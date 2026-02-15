package docker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/idgen"
)

const (
	ContainerStateCreated  = "created"
	ContainerStateRunning  = "running"
	ContainerStatePaused   = "paused"
	ContainerStateStopped  = "stopped"
	ContainerStateExited   = "exited"
	ContainerStateDead     = "dead"
	ContainerStateRemoving = "removing"
)

const (
	mainVolumeName         = "dc2"
	mainVolumePath         = "/dc2"
	mainContainerImageName = "alpine:latest"
	mainContainerName      = "dc2"
	loopDevicePrefix       = "/dev/loop"

	defaultInstanceNetwork = "bridge"
	dc2RuntimeEnvVar       = "DC2_RUNTIME"
	dc2RuntimeHost         = "host"
	dc2RuntimeContainer    = "container"

	imdsNetworkName        = "dc2-imds"
	imdsSubnetCIDR         = "169.254.169.0/24"
	imdsProxyContainerName = "dc2-imds-proxy"
	imdsProxyImageDefault  = "openresty/openresty:1.27.1.2-alpine"
	imdsProxyImageEnvVar   = "DC2_IMDS_PROXY_IMAGE"
	imdsProxyIP            = "169.254.169.254"
	imdsHostAlias          = "host.docker.internal:host-gateway"
	imdsProxyVersionLabel  = "dc2:imds-proxy-version"
	imdsProxyVersion       = "14"
	imdsGatewayResolveWait = 5 * time.Second
	imdsProxyEnsureTimeout = 15 * time.Second
	imdsProxyRetryDelay    = 100 * time.Millisecond
	imdsProxyReadyTimeout  = 60 * time.Second
)

var (
	imdsNetworkMu           sync.RWMutex
	imdsResolvedNetworkName = imdsNetworkName
)

var _ executor.Executor = (*Executor)(nil)

type Executor struct {
	cli                  *client.Client
	mainVolume           volume.Volume
	mainContainerID      string
	dc2RuntimeMode       string
	instanceNetwork      string
	ownsInstanceNetwork  bool
	imdsBackendHostValue string
}

type ExecutorOptions struct {
	IMDSBackendPort int
	InstanceNetwork string
}

func imdsNetwork() string {
	imdsNetworkMu.RLock()
	defer imdsNetworkMu.RUnlock()
	return imdsResolvedNetworkName
}

func setIMDSNetwork(name string) {
	imdsNetworkMu.Lock()
	defer imdsNetworkMu.Unlock()
	imdsResolvedNetworkName = name
}

func ensureIMDSNetwork(ctx context.Context, cli *client.Client) error {
	name := imdsNetwork()
	_, err := cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting IMDS network: %w", err)
	}
	_, err = cli.NetworkCreate(ctx, imdsNetworkName, network.CreateOptions{
		Driver: "bridge",
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{
				{
					Subnet: imdsSubnetCIDR,
				},
			},
		},
	})
	if err == nil || strings.Contains(err.Error(), "already exists") {
		setIMDSNetwork(imdsNetworkName)
		return nil
	}
	if strings.Contains(err.Error(), "Pool overlaps with other one on this address space") {
		networks, listErr := cli.NetworkList(ctx, network.ListOptions{})
		if listErr != nil {
			return fmt.Errorf("listing networks after IMDS overlap error: %w", listErr)
		}
		for _, n := range networks {
			inspect, inspectErr := cli.NetworkInspect(ctx, n.ID, network.InspectOptions{})
			if inspectErr != nil {
				continue
			}
			for _, cfg := range inspect.IPAM.Config {
				if cfg.Subnet == imdsSubnetCIDR {
					setIMDSNetwork(inspect.Name)
					return nil
				}
			}
		}
	}
	if err != nil {
		return fmt.Errorf("creating IMDS network: %w", err)
	}
	return nil
}

func ensureInstanceNetwork(ctx context.Context, cli *client.Client, name string) (bool, error) {
	if name == "" || name == defaultInstanceNetwork {
		return false, nil
	}

	inspect, err := cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return inspect.Labels[LabelDC2OwnedNetwork] == "true", nil
	}
	if !cerrdefs.IsNotFound(err) {
		return false, fmt.Errorf("inspecting instance network %s: %w", name, err)
	}

	_, err = cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			LabelDC2OwnedNetwork: "true",
		},
	})
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		inspect, inspectErr := cli.NetworkInspect(ctx, name, network.InspectOptions{})
		if inspectErr != nil {
			return false, fmt.Errorf("inspecting existing instance network %s: %w", name, inspectErr)
		}
		return inspect.Labels[LabelDC2OwnedNetwork] == "true", nil
	}
	return false, fmt.Errorf("creating instance network %s: %w", name, err)
}

func imdsGatewayIP(ctx context.Context, cli *client.Client) (string, error) {
	inspect, err := cli.NetworkInspect(ctx, imdsNetwork(), network.InspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspecting IMDS network for gateway: %w", err)
	}
	for _, cfg := range inspect.IPAM.Config {
		if strings.TrimSpace(cfg.Gateway) != "" {
			return strings.TrimSpace(cfg.Gateway), nil
		}
	}
	return "", errors.New("IMDS network has no gateway")
}

func resolveLinuxIMDSBackendGateway(ctx context.Context, cli *client.Client) (string, error) {
	deadline := time.Now().Add(imdsGatewayResolveWait)
	lastErr := errors.New("IMDS network has no gateway")
	for time.Now().Before(deadline) {
		gateway, err := imdsGatewayIP(ctx, cli)
		if err == nil && gateway != "" {
			return gateway, nil
		}
		if err != nil {
			lastErr = err
		}
		if sleepErr := sleepWithContext(ctx); sleepErr != nil {
			return "", sleepErr
		}
	}
	return "", fmt.Errorf("resolving IMDS network gateway after %s: %w", imdsGatewayResolveWait, lastErr)
}

func dc2RuntimeEnv(mode string) string {
	return dc2RuntimeEnvVar + "=" + mode
}

func containerEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, variable := range env {
		if value, ok := strings.CutPrefix(variable, prefix); ok {
			return value
		}
	}
	return ""
}

func resolveIMDSBackendHost(ctx context.Context, cli *client.Client) (host string, mode string, err error) {
	hostname, err := os.Hostname()
	if err == nil && strings.TrimSpace(hostname) != "" {
		info, inspectErr := cli.ContainerInspect(ctx, hostname)
		if inspectErr == nil {
			if connectErr := cli.NetworkConnect(ctx, imdsNetwork(), info.ID, nil); connectErr != nil && !strings.Contains(strings.ToLower(connectErr.Error()), "already exists") {
				return "", "", fmt.Errorf("connecting dc2 container %s to IMDS network: %w", info.ID, connectErr)
			}
			updated, updatedErr := cli.ContainerInspect(ctx, info.ID)
			if updatedErr != nil {
				return "", "", fmt.Errorf("inspecting dc2 container %s on IMDS network: %w", info.ID, updatedErr)
			}
			if updated.NetworkSettings == nil || updated.NetworkSettings.Networks == nil {
				return "", "", errors.New("dc2 container has no network settings")
			}
			endpoint := updated.NetworkSettings.Networks[imdsNetwork()]
			if endpoint == nil || strings.TrimSpace(endpoint.IPAddress) == "" {
				return "", "", errors.New("dc2 container has no IMDS network IP")
			}
			return strings.TrimSpace(endpoint.IPAddress), dc2RuntimeContainer, nil
		}
		if !cerrdefs.IsNotFound(inspectErr) {
			return "", "", fmt.Errorf("inspecting potential dc2 container %s: %w", hostname, inspectErr)
		}
	}

	if runtime.GOOS == "linux" {
		gateway, gatewayErr := resolveLinuxIMDSBackendGateway(ctx, cli)
		if gatewayErr != nil {
			return "", "", gatewayErr
		}
		return gateway, dc2RuntimeHost, nil
	}
	return "host.docker.internal", dc2RuntimeHost, nil
}

func ensureIMDSProxyContainer(ctx context.Context, cli *client.Client, imageName string, runtimeMode string) error {
	networkName := imdsNetwork()
	deadline := time.Now().Add(imdsProxyEnsureTimeout)
	attempts := 0

	// Create-first avoids an inspect/create TOCTOU race between concurrent dc2 processes.
	for time.Now().Before(deadline) {
		attempts++
		createdContainerID, created, err := createIMDSProxyContainer(ctx, cli, imageName, runtimeMode)
		if err != nil {
			return err
		}
		if created {
			api.Logger(ctx).Info(
				"created IMDS proxy container",
				slog.String("container_name", imdsProxyContainerName),
				slog.String("container_id", shortenContainerID(createdContainerID)),
				slog.String("image", imageName),
				slog.String("network", networkName),
			)
			if err := startIMDSProxyContainer(ctx, cli, createdContainerID); err != nil {
				if isIMDSProxyEnsureTransientError(err) {
					if sleepErr := sleepWithContext(ctx); sleepErr != nil {
						return fmt.Errorf("retrying IMDS proxy creation: %w", sleepErr)
					}
					continue
				}
				return err
			}
			if err := waitForIMDSProxyReady(ctx, cli, createdContainerID); err != nil {
				if isIMDSProxyEnsureTransientError(err) {
					if sleepErr := sleepWithContext(ctx); sleepErr != nil {
						return fmt.Errorf("retrying IMDS proxy readiness: %w", sleepErr)
					}
					continue
				}
				return err
			}
			api.Logger(ctx).Info(
				"IMDS proxy container is ready",
				slog.String("container_name", imdsProxyContainerName),
				slog.String("container_id", shortenContainerID(createdContainerID)),
				slog.String("mode", "created"),
			)
			return nil
		}

		info, err := cli.ContainerInspect(ctx, imdsProxyContainerName)
		if err != nil {
			if cerrdefs.IsNotFound(err) || isIMDSProxyEnsureTransientError(err) {
				if sleepErr := sleepWithContext(ctx); sleepErr != nil {
					return fmt.Errorf("waiting for IMDS proxy container to appear: %w", sleepErr)
				}
				continue
			}
			return fmt.Errorf("inspecting IMDS proxy container: %w", err)
		}
		if imdsProxyContainerNeedsRecreate(&info, networkName, imageName, runtimeMode) {
			api.Logger(ctx).Info(
				"recreating stale IMDS proxy container",
				slog.String("container_name", imdsProxyContainerName),
				slog.String("container_id", shortenContainerID(info.ID)),
			)
			if removeErr := cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true}); removeErr != nil &&
				!cerrdefs.IsNotFound(removeErr) &&
				!strings.Contains(strings.ToLower(removeErr.Error()), "already in progress") {
				return fmt.Errorf("removing stale IMDS proxy container: %w", removeErr)
			}
			if sleepErr := sleepWithContext(ctx); sleepErr != nil {
				return fmt.Errorf("waiting after removing stale IMDS proxy container: %w", sleepErr)
			}
			continue
		}
		if err := startIMDSProxyContainer(ctx, cli, info.ID); err != nil {
			if isIMDSProxyEnsureTransientError(err) {
				if sleepErr := sleepWithContext(ctx); sleepErr != nil {
					return fmt.Errorf("retrying IMDS proxy start: %w", sleepErr)
				}
				continue
			}
			return err
		}
		if err := waitForIMDSProxyReady(ctx, cli, info.ID); err != nil {
			if isIMDSProxyEnsureTransientError(err) {
				if sleepErr := sleepWithContext(ctx); sleepErr != nil {
					return fmt.Errorf("retrying IMDS proxy readiness: %w", sleepErr)
				}
				continue
			}
			return err
		}
		api.Logger(ctx).Info(
			"IMDS proxy container is ready",
			slog.String("container_name", imdsProxyContainerName),
			slog.String("container_id", shortenContainerID(info.ID)),
			slog.String("mode", "reused"),
		)
		return nil
	}

	return fmt.Errorf("timed out ensuring IMDS proxy container %s after %d attempts", imdsProxyContainerName, attempts)
}

func createIMDSProxyContainer(ctx context.Context, cli *client.Client, imageName string, runtimeMode string) (containerID string, created bool, err error) {
	networkName := imdsNetwork()
	if err := pullImage(ctx, cli, imageName); err != nil {
		return "", false, fmt.Errorf("pulling IMDS proxy image: %w", err)
	}

	configScript := `mkdir -p /etc/nginx/lua /etc/nginx/conf.d
cat >/etc/nginx/nginx.conf <<'EOF'
user root;
worker_processes auto;
events {
  worker_connections 1024;
}
http {
  include /usr/local/openresty/nginx/conf/mime.types;
  default_type application/octet-stream;
  sendfile on;
  keepalive_timeout 65;
  resolver 127.0.0.11 ipv6=off valid=30s;
  include /etc/nginx/conf.d/*.conf;
}
EOF
cat >/etc/nginx/conf.d/default.conf <<'EOF'
server {
  listen 80;
  location /latest/ {
    set $dc2_backend '';
    access_by_lua_file /etc/nginx/lua/dc2_route.lua;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_pass http://$dc2_backend;
  }
}
EOF
cat >/etc/nginx/lua/dc2_route.lua <<'EOF'
local cjson = require "cjson"

local function log_fail(status, message)
  ngx.log(ngx.ERR, "imds proxy routing error: ", message)
  return ngx.exit(status)
end

local function read_http_body(sock, headers)
  local transfer_encoding = headers["transfer-encoding"]
  if transfer_encoding and string.find(string.lower(transfer_encoding), "chunked", 1, true) then
    local chunks = {}
    while true do
      local line, line_err = sock:receive("*l")
      if not line then
        return nil, "reading chunk size: " .. tostring(line_err)
      end
      local chunk_size = tonumber(line, 16)
      if not chunk_size then
        return nil, "invalid chunk size: " .. tostring(line)
      end
      if chunk_size == 0 then
        local _, trailer_err = sock:receive("*l")
        if trailer_err then
          return nil, "reading chunk trailer: " .. tostring(trailer_err)
        end
        break
      end
      local chunk, chunk_err = sock:receive(chunk_size)
      if not chunk then
        return nil, "reading chunk body: " .. tostring(chunk_err)
      end
      chunks[#chunks + 1] = chunk
      local _, crlf_err = sock:receive(2)
      if crlf_err then
        return nil, "reading chunk CRLF: " .. tostring(crlf_err)
      end
    end
    return table.concat(chunks), nil
  end

  local content_length = tonumber(headers["content-length"] or "0")
  if content_length > 0 then
    local body, body_err = sock:receive(content_length)
    if not body then
      return nil, "reading fixed body: " .. tostring(body_err)
    end
    return body, nil
  end

  local parts = {}
  while true do
    local part, part_err, partial = sock:receive(8192)
    if part then
      parts[#parts + 1] = part
    elseif partial and #partial > 0 then
      parts[#parts + 1] = partial
    end
    if part_err == "closed" then
      break
    end
    if part_err then
      return nil, "reading body stream: " .. tostring(part_err)
    end
  end
  return table.concat(parts), nil
end

local function docker_get(path)
  local sock = ngx.socket.tcp()
  sock:settimeout(2000)

  local ok, connect_err = sock:connect("unix:/var/run/docker.sock")
  if not ok then
    return nil, 0, "connecting docker socket: " .. tostring(connect_err)
  end

  local request = "GET " .. path .. " HTTP/1.1\r\nHost: docker\r\nAccept: application/json\r\nConnection: close\r\n\r\n"
  local _, send_err = sock:send(request)
  if send_err then
    sock:close()
    return nil, 0, "sending docker request: " .. tostring(send_err)
  end

  local status_line, status_err = sock:receive("*l")
  if not status_line then
    sock:close()
    return nil, 0, "reading docker status line: " .. tostring(status_err)
  end

  local status = tonumber(string.match(status_line, "^HTTP/%d%.%d%s+(%d+)"))
  if not status then
    sock:close()
    return nil, 0, "invalid docker status line: " .. tostring(status_line)
  end

  local headers = {}
  while true do
    local line, line_err = sock:receive("*l")
    if not line then
      sock:close()
      return nil, 0, "reading docker headers: " .. tostring(line_err)
    end
    if line == "" then
      break
    end
    local key, value = string.match(line, "^(.-):%s*(.*)$")
    if key then
      headers[string.lower(key)] = value
    end
  end

  local body, body_err = read_http_body(sock, headers)
  sock:close()
  if body_err then
    return nil, 0, body_err
  end
  return body, status, nil
end

local containers_body, containers_status, containers_err = docker_get("/containers/json?all=1")
if not containers_body then
  return log_fail(500, containers_err)
end
if containers_status < 200 or containers_status >= 300 then
  return log_fail(500, "listing containers failed with status " .. tostring(containers_status))
end

local containers_ok, containers = pcall(cjson.decode, containers_body)
if not containers_ok or type(containers) ~= "table" then
  return log_fail(500, "invalid container list response")
end

local caller_ip = ngx.var.remote_addr
local instance = nil
for _, info in ipairs(containers) do
  local labels = info.Labels or {}
  if labels["dc2:enabled"] == "true" then
    local networks = ((info.NetworkSettings or {}).Networks) or {}
    for _, endpoint in pairs(networks) do
      if endpoint and endpoint.IPAddress == caller_ip then
        instance = info
        break
      end
    end
  end
  if instance then
    break
  end
end

if not instance then
  return ngx.exit(404)
end

local owner_id = ((instance.Labels or {})["dc2:imds-owner"] or ""):gsub("^%s+", ""):gsub("%s+$", "")
if owner_id == "" then
  return log_fail(500, "instance owner is missing")
end

local owner_body, owner_status, owner_err = docker_get("/containers/" .. owner_id .. "/json")
if not owner_body then
  return log_fail(500, owner_err)
end
if owner_status < 200 or owner_status >= 300 then
  return log_fail(500, "owner inspect failed with status " .. tostring(owner_status))
end

local owner_ok, owner = pcall(cjson.decode, owner_body)
if not owner_ok or type(owner) ~= "table" then
  return log_fail(500, "invalid owner inspect response")
end

local owner_labels = (((owner.Config or {}).Labels) or {})
local backend_host = (owner_labels["dc2:imds-backend-host"] or ""):gsub("^%s+", ""):gsub("%s+$", "")
if backend_host == "" then
  return log_fail(500, "owner backend host is missing")
end

local backend_port = tonumber(owner_labels["dc2:imds-backend-port"] or "")
if not backend_port or backend_port <= 0 then
  return log_fail(500, "owner backend port is invalid")
end

ngx.var.dc2_backend = backend_host .. ":" .. tostring(backend_port)
EOF
exec /usr/local/openresty/bin/openresty -g 'daemon off;' -c /etc/nginx/nginx.conf`

	containerConfig := &container.Config{
		Image: imageName,
		Cmd:   strslice.StrSlice([]string{"sh", "-ceu", configScript}),
		Env:   []string{dc2RuntimeEnv(runtimeMode)},
		Labels: map[string]string{
			imdsProxyVersionLabel: imdsProxyVersion,
		},
	}
	hostConfig := &container.HostConfig{
		ExtraHosts: []string{imdsHostAlias},
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: "/var/run/docker.sock",
				Target: "/var/run/docker.sock",
			},
		},
	}
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {
				IPAMConfig: &network.EndpointIPAMConfig{
					IPv4Address: imdsProxyIP,
				},
			},
		},
	}
	cont, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, imdsProxyContainerName)
	if err == nil {
		return cont.ID, true, nil
	}
	if cerrdefs.IsConflict(err) || strings.Contains(err.Error(), "is already in use") {
		return "", false, nil
	}
	return "", false, fmt.Errorf("creating IMDS proxy container: %w", err)
}

func startIMDSProxyContainer(ctx context.Context, cli *client.Client, containerID string) error {
	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already started") {
			return nil
		}
		return fmt.Errorf("starting IMDS proxy container: %w", err)
	}
	return nil
}

func waitForIMDSProxyReady(ctx context.Context, cli *client.Client, containerID string) error {
	deadline := time.Now().Add(imdsProxyReadyTimeout)
	var (
		attempt         int
		lastExitCode    = -1
		lastProbeErr    error
		lastProbeStdout string
		lastProbeStderr string
	)
	for time.Now().Before(deadline) {
		attempt++
		exitCode, probeStdout, probeStderr, err := execInContainerForExitCode(ctx, cli, containerID, []string{"sh", "-c", "nc -z 127.0.0.1 80"})
		if err == nil && exitCode == 0 {
			return nil
		}
		lastExitCode = exitCode
		lastProbeErr = err
		lastProbeStdout = probeStdout
		lastProbeStderr = probeStderr

		if shouldLogIMDSProbeFailure(attempt) {
			attrs := []any{
				slog.String("container_id", shortenContainerID(containerID)),
				slog.Int("attempt", attempt),
			}
			if err != nil {
				attrs = append(attrs, slog.Any("error", err))
			} else {
				attrs = append(attrs, slog.Int("exit_code", exitCode))
			}
			if strings.TrimSpace(probeStdout) == "" && strings.TrimSpace(probeStderr) == "" {
				diagExitCode, diagStdout, diagStderr, diagErr := execInContainerForExitCode(
					ctx,
					cli,
					containerID,
					[]string{"sh", "-c", "wget -T 1 -O /dev/null http://127.0.0.1/"},
				)
				if diagErr != nil {
					attrs = append(attrs, slog.Any("probe_diag_error", diagErr))
				} else {
					attrs = append(attrs, slog.Int("probe_diag_exit_code", diagExitCode))
				}
				if strings.TrimSpace(diagStdout) != "" {
					attrs = append(attrs, slog.String("probe_diag_stdout", truncateMultiline(diagStdout, 600)))
				}
				if strings.TrimSpace(diagStderr) != "" {
					attrs = append(attrs, slog.String("probe_diag_stderr", truncateMultiline(diagStderr, 600)))
				}
			}
			attrs = append(attrs, slog.String("probe_stdout", printableProbeOutput(probeStdout, 600)))
			attrs = append(attrs, slog.String("probe_stderr", printableProbeOutput(probeStderr, 600)))
			api.Logger(ctx).Warn("IMDS proxy readiness probe failed", attrs...)
		}
		time.Sleep(200 * time.Millisecond)
	}

	details := imdsProxyFailureDetails(cli, containerID) + lastProbeOutputDetails(lastProbeStdout, lastProbeStderr)
	if lastProbeErr != nil {
		return fmt.Errorf(
			"waiting for IMDS proxy container %s readiness timed out after %d attempts: last probe error: %w%s",
			containerID,
			attempt,
			lastProbeErr,
			details,
		)
	}
	if lastExitCode >= 0 {
		return fmt.Errorf(
			"waiting for IMDS proxy container %s readiness timed out after %d attempts: last probe exit code=%d%s",
			containerID,
			attempt,
			lastExitCode,
			details,
		)
	}
	return fmt.Errorf(
		"waiting for IMDS proxy container %s readiness timed out after %d attempts%s",
		containerID,
		attempt,
		details,
	)
}

func execInContainerForExitCode(ctx context.Context, cli *client.Client, containerID string, cmd []string) (int, string, string, error) {
	opts := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}
	createResp, err := cli.ContainerExecCreate(ctx, containerID, opts)
	if err != nil {
		return 0, "", "", fmt.Errorf("creating exec session: %w", err)
	}
	attachResp, err := cli.ContainerExecAttach(ctx, createResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return 0, "", "", fmt.Errorf("attaching exec session: %w", err)
	}
	defer attachResp.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader); err != nil {
		return 0, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), fmt.Errorf("reading exec output: %w", err)
	}
	inspectResp, err := cli.ContainerExecInspect(ctx, createResp.ID)
	if err != nil {
		return 0, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), fmt.Errorf("inspecting exec session: %w", err)
	}
	return inspectResp.ExitCode, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), nil
}

func shouldLogIMDSProbeFailure(attempt int) bool {
	return attempt <= 3 || attempt%10 == 0
}

func shortenContainerID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func imdsProxyFailureDetails(cli *client.Client, containerID string) string {
	var details []string

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		details = append(details, fmt.Sprintf("inspect_error=%v", err))
	} else if info.State != nil {
		details = append(
			details,
			fmt.Sprintf(
				"state(status=%s running=%t restart=%t exit_code=%d error=%q)",
				info.State.Status,
				info.State.Running,
				info.State.Restarting,
				info.State.ExitCode,
				strings.TrimSpace(info.State.Error),
			),
		)
	}

	logs, logErr := tailContainerLogs(ctx, cli, containerID, 80)
	if logErr != nil {
		details = append(details, fmt.Sprintf("logs_error=%v", logErr))
	} else if logs != "" {
		details = append(details, "recent_logs="+truncateMultiline(logs, 4000))
	}

	if len(details) == 0 {
		return ""
	}
	return "\n" + strings.Join(details, "\n")
}

func tailContainerLogs(ctx context.Context, cli *client.Client, containerID string, tail int) (string, error) {
	reader, err := cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: false,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return "", fmt.Errorf("reading container logs: %w", err)
	}
	defer reader.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, reader); err != nil {
		return "", fmt.Errorf("copying container logs: %w", err)
	}

	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())

	switch {
	case out == "" && errOut == "":
		return "", nil
	case out == "":
		return "stderr:\n" + errOut, nil
	case errOut == "":
		return "stdout:\n" + out, nil
	default:
		return "stdout:\n" + out + "\nstderr:\n" + errOut, nil
	}
}

func truncateMultiline(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...<truncated>"
}

func lastProbeOutputDetails(stdout, stderr string) string {
	parts := []string{
		"last_probe_stdout=" + printableProbeOutput(stdout, 2000),
		"last_probe_stderr=" + printableProbeOutput(stderr, 2000),
	}
	return "\n" + strings.Join(parts, "\n")
}

func printableProbeOutput(value string, limit int) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return truncateMultiline(value, limit)
}

func isIMDSProxyEnsureTransientError(err error) bool {
	if err == nil {
		return false
	}
	if cerrdefs.IsNotFound(err) {
		return true
	}
	errLower := strings.ToLower(err.Error())
	return strings.Contains(errLower, "no such container") ||
		strings.Contains(errLower, "not found") && strings.Contains(errLower, "container") ||
		strings.Contains(errLower, "is marked for removal")
}

func sleepWithContext(ctx context.Context) error {
	timer := time.NewTimer(imdsProxyRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func imdsProxyContainerNeedsRecreate(info *container.InspectResponse, networkName string, imageName string, runtimeMode string) bool {
	if info == nil || info.Config == nil || info.NetworkSettings == nil || info.NetworkSettings.Networks == nil {
		return true
	}
	if info.NetworkSettings.Networks[networkName] == nil {
		return true
	}
	if info.Config.Image != imageName {
		return true
	}
	if info.Config.Labels[imdsProxyVersionLabel] != imdsProxyVersion {
		return true
	}
	return containerEnvValue(info.Config.Env, dc2RuntimeEnvVar) != runtimeMode
}

func resolveIMDSProxyImage() string {
	if value := strings.TrimSpace(os.Getenv(imdsProxyImageEnvVar)); value != "" {
		return value
	}
	return imdsProxyImageDefault
}

func NewExecutor(ctx context.Context, opts ExecutorOptions) (*Executor, error) {
	if opts.IMDSBackendPort <= 0 {
		return nil, fmt.Errorf("invalid IMDS backend port %d", opts.IMDSBackendPort)
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}

	pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingContext); err != nil {
		return nil, fmt.Errorf("pinging Docker daemon: %w", err)
	}
	if err := ensureIMDSNetwork(ctx, cli); err != nil {
		return nil, err
	}
	imdsBackendHost, dc2RuntimeMode, err := resolveIMDSBackendHost(ctx, cli)
	if err != nil {
		return nil, fmt.Errorf("resolving IMDS backend host: %w", err)
	}
	imdsProxyImage := resolveIMDSProxyImage()
	instanceNetwork := strings.TrimSpace(opts.InstanceNetwork)
	if instanceNetwork == "" {
		instanceNetwork = defaultInstanceNetwork
	}
	ownsInstanceNetwork, err := ensureInstanceNetwork(ctx, cli, instanceNetwork)
	if err != nil {
		return nil, err
	}

	u, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generating executor suffix: %w", err)
	}
	suffix := "_" + u.String()[:8]

	// Creating an already existing volume is a valid operation
	vol, err := cli.VolumeCreate(ctx, volume.CreateOptions{
		Name: mainVolumeName + suffix,
	})

	if err != nil {
		return nil, fmt.Errorf("creating dc2 master volume")
	}

	id, err := createMainContainer(
		ctx,
		cli,
		mainContainerName+suffix,
		opts.IMDSBackendPort,
		imdsBackendHost,
		dc2RuntimeMode,
		instanceNetwork,
	)
	if err != nil {
		return nil, fmt.Errorf("creating main container: %w", err)
	}
	if err := ensureIMDSProxyContainer(ctx, cli, imdsProxyImage, dc2RuntimeMode); err != nil {
		if removeErr := cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); removeErr != nil && !cerrdefs.IsNotFound(removeErr) {
			slog.Warn("failed to clean up main container after IMDS initialization failure", slog.String("container_id", id), slog.Any("error", removeErr))
		}
		if removeErr := cli.VolumeRemove(ctx, vol.Name, true); removeErr != nil && !cerrdefs.IsNotFound(removeErr) {
			slog.Warn("failed to clean up main volume after IMDS initialization failure", slog.String("volume", vol.Name), slog.Any("error", removeErr))
		}
		if ownsInstanceNetwork {
			if removeErr := cli.NetworkRemove(ctx, instanceNetwork); removeErr != nil && !cerrdefs.IsNotFound(removeErr) {
				slog.Warn("failed to clean up instance network after IMDS initialization failure", slog.String("network", instanceNetwork), slog.Any("error", removeErr))
			}
		}
		return nil, fmt.Errorf("initializing IMDS infrastructure: %w", err)
	}

	return &Executor{
		cli:                  cli,
		mainVolume:           vol,
		mainContainerID:      id,
		dc2RuntimeMode:       dc2RuntimeMode,
		instanceNetwork:      instanceNetwork,
		ownsInstanceNetwork:  ownsInstanceNetwork,
		imdsBackendHostValue: imdsBackendHost,
	}, nil
}

func (e *Executor) Close(ctx context.Context) error {
	var closeErr error
	ignoreMainContainerID := e.mainContainerID
	if err := e.cli.ContainerRemove(ctx, e.mainContainerID, container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		ignoreMainContainerID = ""
		closeErr = errors.Join(
			closeErr,
			fmt.Errorf("removing main container %s: %w", e.mainContainerID, err),
		)
	}
	if err := e.cli.VolumeRemove(ctx, e.mainVolume.Name, true); err != nil && !cerrdefs.IsNotFound(err) {
		closeErr = errors.Join(closeErr, fmt.Errorf("removing main volume %s: %w", e.mainContainerID, err))
	}
	if err := e.removeIMDSProxyIfUnused(ctx, ignoreMainContainerID); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := e.removeInstanceNetworkIfUnused(ctx, ignoreMainContainerID); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := e.Disconnect(); err != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("closing Docker client: %w", err))
	}
	return closeErr
}

func (e *Executor) Disconnect() error {
	if e.cli == nil {
		return nil
	}
	return e.cli.Close()
}

func (e *Executor) ListOwnedInstances(ctx context.Context) ([]executor.InstanceID, error) {
	containers, err := e.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelDC2Enabled+"=true"),
			filters.Arg("label", LabelDC2IMDSOwner+"="+e.mainContainerID),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("listing owned instances: %w", err)
	}
	ids := make([]executor.InstanceID, 0, len(containers))
	for _, c := range containers {
		instanceID, ok := c.Labels[LabelDC2InstanceID]
		if !ok || strings.TrimSpace(instanceID) == "" {
			return nil, fmt.Errorf("owned instance container %s is missing %s label", c.ID, LabelDC2InstanceID)
		}
		ids = append(ids, executor.InstanceID(strings.TrimSpace(instanceID)))
	}
	slices.SortFunc(ids, func(a, b executor.InstanceID) int {
		return strings.Compare(string(a), string(b))
	})
	return ids, nil
}

func (e *Executor) removeIMDSProxyIfUnused(ctx context.Context, ignoreMainContainerID string) error {
	mainContainers, err := e.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelDC2Main+"=true"),
		),
	})
	if err != nil {
		return fmt.Errorf("listing dc2 main containers: %w", err)
	}
	for _, mainContainer := range mainContainers {
		if ignoreMainContainerID != "" && mainContainer.ID == ignoreMainContainerID {
			continue
		}
		return nil
	}

	info, err := e.cli.ContainerInspect(ctx, imdsProxyContainerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspecting IMDS proxy container: %w", err)
	}
	if err := e.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("removing IMDS proxy container: %w", err)
	}
	api.Logger(ctx).Info(
		"removed IMDS proxy container",
		slog.String("container_name", imdsProxyContainerName),
		slog.String("container_id", shortenContainerID(info.ID)),
	)
	return nil
}

func (e *Executor) removeInstanceNetworkIfUnused(ctx context.Context, ignoreMainContainerID string) error {
	if !e.ownsInstanceNetwork || e.instanceNetwork == "" || e.instanceNetwork == defaultInstanceNetwork {
		return nil
	}
	mainContainers, err := e.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelDC2Main+"=true"),
			filters.Arg("label", LabelDC2InstanceNet+"="+e.instanceNetwork),
		),
	})
	if err != nil {
		return fmt.Errorf("listing dc2 main containers for network cleanup: %w", err)
	}
	for _, mainContainer := range mainContainers {
		if ignoreMainContainerID != "" && mainContainer.ID == ignoreMainContainerID {
			continue
		}
		return nil
	}
	if err := e.cli.NetworkRemove(ctx, e.instanceNetwork); err != nil {
		errLower := strings.ToLower(err.Error())
		if cerrdefs.IsNotFound(err) || strings.Contains(errLower, "active endpoints") {
			return nil
		}
		return fmt.Errorf("removing instance network %s: %w", e.instanceNetwork, err)
	}
	return nil
}

func (e *Executor) CreateInstances(ctx context.Context, req executor.CreateInstancesRequest) ([]executor.InstanceID, error) {
	if err := pullImage(ctx, e.cli, req.ImageID); err != nil {
		return nil, fmt.Errorf("pulling image: %w", err)
	}
	instanceIDs := make([]executor.InstanceID, req.Count)
	for i := range req.Count {
		instanceID, err := idgen.Hex(idgen.AWSLikeHexIDLength)
		if err != nil {
			return nil, fmt.Errorf("generating instance id: %w", err)
		}
		labels := map[string]string{
			LabelDC2Enabled:      "true",
			LabelDC2InstanceID:   instanceID,
			LabelDC2InstanceType: req.InstanceType,
			LabelDC2ImageID:      req.ImageID,
			LabelDC2IMDSOwner:    e.mainContainerID,
		}
		if req.UserData != "" {
			labels[LabelDC2UserData] = req.UserData
		}

		containerConfig := &container.Config{
			Image:  req.ImageID,
			Env:    []string{dc2RuntimeEnv(e.dc2RuntimeMode)},
			Labels: labels,
		}
		hostConfig := &container.HostConfig{
			// Allow mounting block devices to attach volumes
			Privileged: true,
			Mounts:     dc2Mounts(),
		}
		if e.instanceNetwork != "" && e.instanceNetwork != defaultInstanceNetwork {
			hostConfig.NetworkMode = container.NetworkMode(e.instanceNetwork)
		}
		networkingConfig := &network.NetworkingConfig{}
		cont, err := e.cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, "")
		if err != nil {
			return nil, fmt.Errorf("creating container: %w", err)
		}
		if err := e.cli.NetworkConnect(ctx, imdsNetwork(), cont.ID, nil); err != nil && !strings.Contains(err.Error(), "already exists") {
			return nil, fmt.Errorf("connecting instance %s to IMDS network: %w", cont.ID, err)
		}
		instanceIDs[i] = executor.InstanceID(instanceID)
	}
	return instanceIDs, nil
}

func (e *Executor) DescribeInstances(ctx context.Context, req executor.DescribeInstancesRequest) ([]executor.InstanceDescription, error) {
	var descriptions []executor.InstanceDescription
	for _, id := range req.InstanceIDs {
		info, err := e.findContainer(ctx, id)
		if err != nil {
			// Specifying non-existing IDs is not an error
			var apiErr *api.Error
			if errors.As(err, &apiErr) && apiErr.Code == api.ErrorCodeInstanceNotFound {
				continue
			}
			return nil, fmt.Errorf("getting spec for instance %s: %w", id, err)
		}
		desc, err := e.instanceDescription(ctx, info)
		if err != nil {
			return nil, err
		}
		descriptions = append(descriptions, desc)
	}
	return descriptions, nil
}

func (e *Executor) StartInstances(ctx context.Context, req executor.StartInstancesRequest) ([]executor.InstanceStateChange, error) {
	containers, err := e.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	changes := make([]executor.InstanceStateChange, len(containers))
	for i, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if err := e.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
			return nil, fmt.Errorf("starting instance %s: %w", c.ID, err)
		}
		info, err := e.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("inspecting container %s: %w", c.ID, err)
		}
		currentState, err := instanceState(info.State)
		if err != nil {
			return nil, fmt.Errorf("determining current state for instance %s: %w", c.ID, err)
		}
		instanceID, err := instanceIDFromContainer(c)
		if err != nil {
			return nil, err
		}
		changes[i] = executor.InstanceStateChange{
			InstanceID:    instanceID,
			PreviousState: previousState,
			CurrentState:  currentState,
		}
	}
	return changes, nil
}

func (e *Executor) StopInstances(ctx context.Context, req executor.StopInstancesRequest) ([]executor.InstanceStateChange, error) {
	containers, err := e.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	var timeout *int
	if req.Force {
		zero := 0
		timeout = &zero
	}
	changes := make([]executor.InstanceStateChange, len(containers))
	for i, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if err := e.cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: timeout}); err != nil {
			return nil, fmt.Errorf("stopping instance %s: %w", c.ID, err)
		}
		info, err := e.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("inspecting container %s: %w", c.ID, err)
		}
		currentState, err := instanceState(info.State)
		if err != nil {
			return nil, fmt.Errorf("determining current state for instance %s: %w", c.ID, err)
		}
		instanceID, err := instanceIDFromContainer(c)
		if err != nil {
			return nil, err
		}
		changes[i] = executor.InstanceStateChange{
			InstanceID:    instanceID,
			PreviousState: previousState,
			CurrentState:  currentState,
		}
	}
	return changes, nil
}

func (e *Executor) TerminateInstances(ctx context.Context, req executor.TerminateInstancesRequest) ([]executor.InstanceStateChange, error) {
	containers, err := e.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	changes := make([]executor.InstanceStateChange, len(containers))
	for i, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if c.State.Running {
			if err := e.cli.ContainerStop(ctx, c.ID, container.StopOptions{}); err != nil {
				return nil, fmt.Errorf("stopping instance %s: %w", c.ID, err)
			}
		}
		if err := e.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{}); err != nil {
			return nil, fmt.Errorf("removing instance %s: %w", c.ID, err)
		}
		instanceID, err := instanceIDFromContainer(c)
		if err != nil {
			return nil, err
		}

		changes[i] = executor.InstanceStateChange{
			InstanceID:    instanceID,
			PreviousState: previousState,
			CurrentState:  api.InstanceStateTerminated,
		}
	}
	return changes, nil
}

func (e *Executor) CreateVolume(ctx context.Context, req executor.CreateVolumeRequest) (executor.VolumeID, error) {
	id, err := idgen.Hex(idgen.AWSLikeHexIDLength)
	if err != nil {
		return "", fmt.Errorf("generating volume id: %w", err)
	}
	volumeID := executor.VolumeID(id)
	volumeFileCmd := []string{"truncate", "-s", strconv.FormatInt(req.Size, 10), internalVolumeFilePath(volumeID)}
	if _, _, err := e.execInMainContainer(ctx, volumeFileCmd); err != nil {
		return "", fmt.Errorf("executing command to create volume file: %w", err)
	}
	attachmentsFileCmd := []string{"touch", internalVolumeAttachmentInfoPath(volumeID)}
	if _, _, err := e.execInMainContainer(ctx, attachmentsFileCmd); err != nil {
		return "", fmt.Errorf("executing command to create volume attachments file: %w", err)
	}
	return volumeID, nil
}

func (e *Executor) DeleteVolume(ctx context.Context, req executor.DeleteVolumeRequest) error {
	deleteVolumeCmd := []string{"rm", internalVolumeFilePath(req.VolumeID)}
	if _, _, err := e.execInMainContainer(ctx, deleteVolumeCmd); err != nil {
		return fmt.Errorf("executing command to delete volume: %w", err)
	}
	deleteAttachmentsCmd := []string{"rm", "-f", internalVolumeAttachmentInfoPath(req.VolumeID)}
	if _, _, err := e.execInMainContainer(ctx, deleteAttachmentsCmd); err != nil {
		return fmt.Errorf("executing command to delete volume attachments: %w", err)
	}
	return nil
}

func (e *Executor) AttachVolume(ctx context.Context, req executor.AttachVolumeRequest) (*executor.VolumeAttachment, error) {
	instanceContainer, err := e.findContainer(ctx, req.InstanceID)
	if err != nil {
		return nil, err
	}
	nextLoopDevice, _, err := e.execInContainer(ctx, instanceContainer.ID, []string{"losetup", "-f"})
	if err != nil {
		return nil, fmt.Errorf("find next available loop device: %w", err)
	}
	nextLoopDevice = strings.TrimSpace(nextLoopDevice)
	if parts := strings.Fields(nextLoopDevice); len(parts) > 0 {
		nextLoopDevice = parts[0]
	}
	if !strings.HasPrefix(nextLoopDevice, loopDevicePrefix) {
		return nil, fmt.Errorf("unknown loop device %q", nextLoopDevice)
	}
	num, err := strconv.Atoi(strings.TrimSpace((nextLoopDevice[len(loopDevicePrefix):])))
	if err != nil {
		return nil, fmt.Errorf("invalid loop device number: %w", err)
	}
	deviceCmd := []string{
		"mknod",
		req.Device,
		"b",               // block device
		"7",               // major number for loop devices
		strconv.Itoa(num), // next available one
	}
	if _, _, err := e.execInContainer(ctx, instanceContainer.ID, deviceCmd); err != nil {
		return nil, fmt.Errorf("creating device %s: %w", req.Device, err)
	}
	setupCmd := []string{"losetup", req.Device, internalVolumeFilePath(req.VolumeID)}
	if _, _, err := e.execInContainer(ctx, instanceContainer.ID, setupCmd); err != nil {
		return nil, fmt.Errorf("setting up device device %s: %w", req.Device, err)
	}
	// Record the attachment
	info := deviceAttachment{
		InstanceID:    req.InstanceID,
		Device:        req.Device,
		LoopDeviceNum: num,
		AttachTime:    time.Now(),
	}
	if err := e.recordAttachment(ctx, req.VolumeID, info); err != nil {
		return nil, fmt.Errorf("recording attachment: %w", err)
	}
	return &executor.VolumeAttachment{
		Device:     req.Device,
		InstanceID: req.InstanceID,
		AttachTime: info.AttachTime,
	}, nil
}

func (e *Executor) DetachVolume(ctx context.Context, req executor.DetachVolumeRequest) (*executor.VolumeAttachment, error) {
	instanceContainer, err := e.findContainer(ctx, req.InstanceID)
	if err != nil {
		return nil, err
	}
	var attachment *deviceAttachment
	atts, err := e.findVolumeAttachments(ctx, req.VolumeID)
	if err != nil {
		return nil, fmt.Errorf("finding volume attachments: %w", err)
	}
	for _, a := range atts {
		if a.InstanceID == req.InstanceID && a.Device == req.Device {
			attachment = &a
			break
		}
	}
	if attachment == nil {
		return nil, fmt.Errorf("volume %s not attached to instance %s on device %s", req.VolumeID, req.InstanceID, req.Device)
	}
	losetupCmd := []string{"losetup", "-d", attachment.Device}
	if _, _, err := e.execInContainer(ctx, instanceContainer.ID, losetupCmd); err != nil {
		return nil, fmt.Errorf("removing loopback device %s: %w", req.Device, err)
	}
	deviceCmd := []string{"rm", "-f", attachment.Device}
	if _, _, err := e.execInContainer(ctx, instanceContainer.ID, deviceCmd); err != nil {
		return nil, fmt.Errorf("removing dev device %s: %w", req.Device, err)
	}
	if err := e.deleteAttachment(ctx, req.VolumeID, *attachment); err != nil {
		return nil, fmt.Errorf("deleting attachment info: %w", err)
	}
	return &executor.VolumeAttachment{
		Device:     req.Device,
		InstanceID: req.InstanceID,
		AttachTime: attachment.AttachTime,
	}, nil
}

func (e *Executor) DescribeVolumes(ctx context.Context, req executor.DescribeVolumesRequest) ([]executor.VolumeDescription, error) {
	descs := make([]executor.VolumeDescription, len(req.VolumeIDs))
	for i, id := range req.VolumeIDs {
		cmd := []string{"du", "-b", internalVolumeFilePath(id)}
		stdout, _, err := e.execInMainContainer(ctx, cmd)
		if err != nil {
			return nil, err
		}
		before, _, ok := strings.Cut(stdout, "\t")
		if !ok {
			return nil, fmt.Errorf("invalid du output %q", stdout)
		}
		n, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid du output %q: %w", stdout, err)
		}
		atts, err := e.findVolumeAttachments(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("finding volume attachments: %w", err)
		}
		attachments := make([]executor.VolumeAttachment, len(atts))
		for i, a := range atts {
			attachments[i] = executor.VolumeAttachment{
				InstanceID: a.InstanceID,
				Device:     a.Device,
				AttachTime: a.AttachTime,
			}
		}
		descs[i] = executor.VolumeDescription{
			VolumeID:    id,
			Size:        n,
			Attachments: attachments,
		}
	}
	return descs, nil
}

func instanceIDFromContainer(info *container.InspectResponse) (executor.InstanceID, error) {
	if info == nil || info.Config == nil {
		return "", errors.New("instance container metadata is missing")
	}
	instanceID := strings.TrimSpace(info.Config.Labels[LabelDC2InstanceID])
	if instanceID == "" {
		return "", fmt.Errorf("instance container %s is missing %s label", info.ID, LabelDC2InstanceID)
	}
	return executor.InstanceID(instanceID), nil
}

func (e *Executor) findContainer(ctx context.Context, instanceID executor.InstanceID) (*container.InspectResponse, error) {
	containers, err := e.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelDC2Enabled+"=true"),
			filters.Arg("label", LabelDC2InstanceID+"="+string(instanceID)),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("listing container for instance %s: %w", instanceID, err)
	}
	if len(containers) == 0 {
		return nil, api.ErrWithCode(api.ErrorCodeInstanceNotFound, fmt.Errorf("instance %s doesn't exist", instanceID))
	}
	if len(containers) > 1 {
		return nil, fmt.Errorf("found %d containers for instance %s", len(containers), instanceID)
	}
	info, err := e.cli.ContainerInspect(ctx, containers[0].ID)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, api.ErrWithCode(api.ErrorCodeInstanceNotFound, fmt.Errorf("instance %s doesn't exist: %w", instanceID, err))
		}
		return nil, fmt.Errorf("retrieving container for instance %s: %w", instanceID, err)
	}
	if !isDc2Container(info) {
		return nil, api.ErrWithCode(api.ErrorCodeInstanceNotFound, fmt.Errorf("instance %s doesn't exist", instanceID))
	}
	return &info, nil
}

func (e *Executor) findContainers(ctx context.Context, instanceIDs []executor.InstanceID) ([]*container.InspectResponse, error) {
	var containers []*container.InspectResponse
	// Validate all the instances first
	for _, id := range instanceIDs {
		info, err := e.findContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		containers = append(containers, info)
	}
	return containers, nil
}

func (e *Executor) instanceDescription(ctx context.Context, info *container.InspectResponse) (executor.InstanceDescription, error) {
	created, err := time.Parse(time.RFC3339Nano, info.Created)
	if err != nil {
		return executor.InstanceDescription{}, fmt.Errorf("parsing container creation time: %w", err)
	}
	labels := info.Config.Labels
	image, err := e.cli.ImageInspect(ctx, info.Image)
	if err != nil {
		return executor.InstanceDescription{}, fmt.Errorf("inspecting image: %w", err)
	}
	imageID := labels[LabelDC2ImageID]
	state, err := instanceState(info.State)
	if err != nil {
		return executor.InstanceDescription{}, fmt.Errorf("instance state: %w", err)
	}
	instanceType := labels[LabelDC2InstanceType]
	// First character in c.Name is /
	dnsName := info.Name[1:]
	privateIP := primaryContainerIPv4Address(info, imdsNetwork())
	// We expose the same reachable container address for both private/public
	// fields so EC2 clients expecting PublicIpAddress can operate in tests.
	publicIP := privateIP
	healthStatus := executor.InstanceHealthStatusUnknown
	if info.State != nil && info.State.Health != nil {
		healthStatus = executor.InstanceHealthStatus(strings.ToLower(strings.TrimSpace(info.State.Health.Status)))
	}
	instanceID, err := instanceIDFromContainer(info)
	if err != nil {
		return executor.InstanceDescription{}, err
	}
	return executor.InstanceDescription{
		InstanceID:     instanceID,
		ImageID:        imageID,
		InstanceState:  state,
		HealthStatus:   healthStatus,
		PrivateDNSName: dnsName,
		PrivateIP:      privateIP,
		PublicIP:       publicIP,
		InstanceType:   instanceType,
		Architecture:   awsArchFromDockerArch(image.Architecture),
		LaunchTime:     created,
	}, nil
}

func primaryContainerIPv4Address(info *container.InspectResponse, excludedNetwork string) string {
	if info.NetworkSettings == nil || len(info.NetworkSettings.Networks) == 0 {
		return ""
	}
	networkNames := slices.Collect(maps.Keys(info.NetworkSettings.Networks))
	slices.Sort(networkNames)
	for _, networkName := range networkNames {
		if networkName == excludedNetwork {
			continue
		}
		settings := info.NetworkSettings.Networks[networkName]
		if settings != nil && settings.IPAddress != "" {
			return settings.IPAddress
		}
	}
	for _, networkName := range networkNames {
		settings := info.NetworkSettings.Networks[networkName]
		if settings != nil && settings.IPAddress != "" {
			return settings.IPAddress
		}
	}
	return ""
}

func (e *Executor) execInMainContainer(ctx context.Context, cmd []string) (string, string, error) {
	return e.execInContainer(ctx, e.mainContainerID, cmd)
}

func (e *Executor) execInContainer(ctx context.Context, containerID string, cmd []string) (string, string, error) {
	opts := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}
	createResp, err := e.cli.ContainerExecCreate(ctx, containerID, opts)
	if err != nil {
		return "", "", fmt.Errorf("creating exec session: %w", err)
	}

	attachResp, err := e.cli.ContainerExecAttach(ctx, createResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", "", fmt.Errorf("attaching to exec session: %w", err)
	}
	defer attachResp.Close()

	var stdout, stderr bytes.Buffer

	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		return "", "", fmt.Errorf("reading exec session: %w", err)
	}

	inspectResp, err := e.cli.ContainerExecInspect(ctx, createResp.ID)
	if err != nil {
		return "", "", fmt.Errorf("inspecting exec session: %w", err)
	}
	if inspectResp.ExitCode != 0 {
		return "", "", fmt.Errorf("exec session returned non-zero exit code %d: stdout: %s, stderr: %s", inspectResp.ExitCode, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), nil
}

func (e *Executor) recordAttachment(ctx context.Context, vol executor.VolumeID, info deviceAttachment) error {
	recordCmd := []string{"sh", "-c", fmt.Sprintf("echo %s >> %s", info.String(), internalVolumeAttachmentInfoPath(vol))}
	if _, _, err := e.execInMainContainer(ctx, recordCmd); err != nil {
		return fmt.Errorf("recording attachment: %w", err)
	}
	return nil
}

func (e *Executor) deleteAttachment(ctx context.Context, vol executor.VolumeID, info deviceAttachment) error {
	deleteCmd := []string{"sh", "-c", fmt.Sprintf("sed -i '#%s#d' %s", info.String(), internalVolumeAttachmentInfoPath(vol))}
	if _, _, err := e.execInMainContainer(ctx, deleteCmd); err != nil {
		return fmt.Errorf("deleting attachment: %w", err)
	}
	return nil
}

func (e *Executor) findVolumeAttachments(ctx context.Context, vol executor.VolumeID) ([]deviceAttachment, error) {
	stdout, _, err := e.execInMainContainer(ctx, []string{"cat", internalVolumeAttachmentInfoPath(vol)})
	if err != nil {
		return nil, fmt.Errorf("reading volume attachments: %w", err)
	}
	var attachments []deviceAttachment
	r := bufio.NewScanner(strings.NewReader(stdout))
	for r.Scan() {
		line := r.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 4 {
			return nil, fmt.Errorf("bad volume attachment info %q", line)
		}
		loopDeviceNum, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("parsing loop device number: %w", err)
		}
		attachTimeUnixNano, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing volume attach time: %w", err)
		}
		info := deviceAttachment{
			InstanceID:    executor.InstanceID(parts[0]),
			Device:        parts[1],
			LoopDeviceNum: loopDeviceNum,
			AttachTime:    time.Unix(0, attachTimeUnixNano),
		}
		attachments = append(attachments, info)
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("scanning volume attachments: %w", err)
	}
	return attachments, nil
}

func instanceState(state *container.State) (api.InstanceState, error) {
	if state == nil {
		return api.InstanceState{}, errors.New("nil container state")
	}

	switch {
	case state.Status == "created":
		return api.InstanceStatePending, nil
	case state.Running && !state.Paused:
		return api.InstanceStateRunning, nil
	case state.Paused:
		return api.InstanceStateStopping, nil
	case state.Status == "exited":
		return api.InstanceStateStopped, nil
	case state.Dead:
		return api.InstanceStateTerminated, nil
	case state.Status == "removing":
		return api.InstanceStateShuttingDown, nil
	default:
		return api.InstanceState{}, errors.New("unknown container state")
	}
}

func awsArchFromDockerArch(arch string) string {
	if arch == "amd64" {
		return "x86_64"
	}
	return arch
}

func createMainContainer(
	ctx context.Context,
	cli *client.Client,
	name string,
	imdsBackendPort int,
	imdsBackendHost string,
	runtimeMode string,
	instanceNetwork string,
) (string, error) {
	if err := pullImage(ctx, cli, mainContainerImageName); err != nil {
		return "", fmt.Errorf("pulling image for main container: %w", err)
	}

	labels := map[string]string{
		LabelDC2Main:     "true",
		LabelDC2IMDSHost: imdsBackendHost,
		LabelDC2IMDSPort: strconv.Itoa(imdsBackendPort),
	}
	if instanceNetwork != "" && instanceNetwork != defaultInstanceNetwork {
		labels[LabelDC2InstanceNet] = instanceNetwork
	}
	containerConfig := &container.Config{
		Image:  mainContainerImageName,
		Cmd:    strslice.StrSlice([]string{"sleep", "infinity"}),
		Env:    []string{dc2RuntimeEnv(runtimeMode)},
		Labels: labels,
	}
	hostConfig := &container.HostConfig{
		AutoRemove: true,
		Mounts:     dc2Mounts(),
	}
	networkingConfig := &network.NetworkingConfig{}
	cont, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, name)
	if err != nil {
		return "", fmt.Errorf("creating main container: %w", err)
	}

	if err := cli.ContainerStart(ctx, cont.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("starting main container: %w", err)
	}

	return cont.ID, nil
}

func pullImage(ctx context.Context, cli *client.Client, imageName string) error {
	api.Logger(ctx).Debug("pulling image", slog.String("name", imageName))
	if _, err := cli.ImageInspect(ctx, imageName); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting local image %s: %w", imageName, err)
	}
	pullProgress, err := cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("starting pull for %s: %w", imageName, err)
	}
	if _, err := io.ReadAll(pullProgress); err != nil {
		pullProgress.Close()
		return fmt.Errorf("pulling %s: %w", imageName, err)
	}
	if err := pullProgress.Close(); err != nil {
		return fmt.Errorf("finalizing pull for %s: %w", imageName, err)
	}
	return nil
}

func dc2Mounts() []mount.Mount {
	return []mount.Mount{
		{
			Type:   mount.TypeVolume,
			Source: mainVolumeName,
			Target: mainVolumePath,
		},
	}
}

func internalVolumeFilePath(id executor.VolumeID) string {
	return fmt.Sprintf("%s/%s", mainVolumePath, id)
}

type deviceAttachment struct {
	InstanceID    executor.InstanceID
	Device        string
	LoopDeviceNum int
	AttachTime    time.Time
}

func (i *deviceAttachment) String() string {
	return fmt.Sprintf("%s:%s:%d:%d", i.InstanceID, i.Device, i.LoopDeviceNum, i.AttachTime.UnixNano())
}

func internalVolumeAttachmentInfoPath(id executor.VolumeID) string {
	return fmt.Sprintf("%s.attachments", internalVolumeFilePath(id))
}
