package dc2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/fiam/dc2/pkg/dc2/docker"
	"github.com/fiam/dc2/pkg/dc2/executor"
)

const (
	// Must match paths configured in pkg/dc2/docker/executor.go.
	imdsSocketDir      = "/tmp/dc2-imds"
	imdsSocketFilename = "backend.sock"
	imdsSocketPath     = imdsSocketDir + "/" + imdsSocketFilename
	imdsPortFilePath   = imdsSocketDir + "/backend.port"
	imdsBackendAddr    = "0.0.0.0:0"

	imdsTokenHeader    = "X-aws-ec2-metadata-token"
	imdsTokenTTLHeader = "X-aws-ec2-metadata-token-ttl-seconds"

	imdsTokenMinTTLSeconds = 1
	imdsTokenMaxTTLSeconds = 21600
	imdsTokenBytes         = 32

	imdsInternalHealthPath  = "/_dc2/internal/healthz"
	imdsInternalProxyPort   = "/_dc2/internal/proxy-port"
	imdsInternalPathPrefix  = "/_dc2/internal/instances/"
	imdsMetadataTagsBaseURL = "/latest/meta-data/tags/instance"
)

var (
	imdsServerOnce sync.Once
	imdsServerErr  error

	imdsDisabledInstances sync.Map
	imdsTokens            sync.Map
	imdsInstanceTags      sync.Map

	imdsProxyPortMu sync.RWMutex
	imdsProxyPort   int

	errIMDSBackendAlreadyRunning = errors.New("imds backend already running")

	imdsControlClient = &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 3 * time.Second}
				return d.DialContext(ctx, "unix", imdsSocketPath)
			},
		},
	}
)

var errIMDSInstanceNotFound = errors.New("imds instance not found")

type imdsToken struct {
	containerID string
	expiresAt   time.Time
}

func ensureIMDSServer() error {
	imdsServerOnce.Do(func() {
		if err := os.MkdirAll(imdsSocketDir, 0o755); err != nil {
			imdsServerErr = fmt.Errorf("creating IMDS socket directory: %w", err)
			return
		}
		if imdsBackendHealthy(context.Background()) {
			if port, err := imdsProxyPortFromBackend(context.Background()); err == nil {
				_ = writeIMDSProxyPort(port)
			}
			return
		}

		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			imdsServerErr = fmt.Errorf("creating Docker client: %w", err)
			return
		}

		unixListener, err := listenIMDSSocket()
		if errors.Is(err, errIMDSBackendAlreadyRunning) {
			if port, portErr := imdsProxyPortFromBackend(context.Background()); portErr == nil {
				_ = writeIMDSProxyPort(port)
			}
			return
		}
		if err != nil {
			imdsServerErr = fmt.Errorf("binding IMDS backend listener: %w", err)
			return
		}
		tcpListener, err := net.Listen("tcp", imdsBackendAddr)
		if err != nil {
			_ = unixListener.Close()
			imdsServerErr = fmt.Errorf("binding IMDS backend TCP listener: %w", err)
			return
		}
		tcpAddr, ok := tcpListener.Addr().(*net.TCPAddr)
		if !ok || tcpAddr.Port == 0 {
			_ = unixListener.Close()
			_ = tcpListener.Close()
			imdsServerErr = fmt.Errorf("resolving IMDS backend TCP address: %v", tcpListener.Addr())
			return
		}
		setIMDSProxyPort(tcpAddr.Port)
		if err := writeIMDSProxyPort(tcpAddr.Port); err != nil {
			_ = unixListener.Close()
			_ = tcpListener.Close()
			imdsServerErr = fmt.Errorf("persisting IMDS backend TCP port: %w", err)
			return
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/latest/api/token", func(w http.ResponseWriter, r *http.Request) {
			handleIMDSToken(w, r, cli)
		})
		mux.HandleFunc("/latest/meta-data/instance-id", func(w http.ResponseWriter, r *http.Request) {
			handleIMDSInstanceID(w, r, cli)
		})
		mux.HandleFunc("/latest/user-data", func(w http.ResponseWriter, r *http.Request) {
			handleIMDSUserData(w, r, cli)
		})
		mux.HandleFunc(imdsMetadataTagsBaseURL, func(w http.ResponseWriter, r *http.Request) {
			handleIMDSInstanceTagKeys(w, r, cli)
		})
		mux.HandleFunc(imdsMetadataTagsBaseURL+"/", func(w http.ResponseWriter, r *http.Request) {
			handleIMDSInstanceTagValue(w, r, cli)
		})

		// Internal control plane over local unix socket. Not exposed to instance traffic.
		mux.HandleFunc(imdsInternalHealthPath, handleIMDSInternalHealth)
		mux.HandleFunc(imdsInternalProxyPort, handleIMDSInternalProxyPort)
		mux.HandleFunc(imdsInternalPathPrefix, handleIMDSInternalInstanceControl)

		server := &http.Server{
			Handler: mux,
		}
		serve := func(listener net.Listener, listenerType string) {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error(
					"IMDS backend server failed",
					slog.String("listener", listenerType),
					slog.Any("error", err),
				)
			}
		}
		go serve(unixListener, "unix")
		go serve(tcpListener, "tcp")
	})
	return imdsServerErr
}

func listenIMDSSocket() (net.Listener, error) {
	ln, err := net.Listen("unix", imdsSocketPath)
	if err == nil {
		return ln, nil
	}
	if !strings.Contains(strings.ToLower(err.Error()), "address already in use") {
		return nil, err
	}
	if imdsBackendHealthy(context.Background()) {
		return nil, errIMDSBackendAlreadyRunning
	}
	if removeErr := os.Remove(imdsSocketPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return nil, fmt.Errorf("removing stale IMDS socket: %w", removeErr)
	}
	return net.Listen("unix", imdsSocketPath)
}

func imdsBackendHealthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://imds"+imdsInternalHealthPath, nil)
	if err != nil {
		return false
	}
	resp, err := imdsControlClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func handleIMDSInstanceID(w http.ResponseWriter, r *http.Request, cli *client.Client) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := resolveMetadataRequest(w, r, cli)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(apiInstanceID(executor.InstanceID(info.ID))))
}

func handleIMDSUserData(w http.ResponseWriter, r *http.Request, cli *client.Client) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := resolveMetadataRequest(w, r, cli)
	if !ok {
		return
	}
	userData := info.Config.Labels[docker.LabelDC2UserData]
	if userData == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(userData))
}

func handleIMDSInstanceTagKeys(w http.ResponseWriter, r *http.Request, cli *client.Client) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := resolveMetadataRequest(w, r, cli)
	if !ok {
		return
	}
	tags := imdsTags(info.ID)
	if len(tags) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(strings.Join(keys, "\n")))
}

func handleIMDSInstanceTagValue(w http.ResponseWriter, r *http.Request, cli *client.Client) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := resolveMetadataRequest(w, r, cli)
	if !ok {
		return
	}
	keyPath := strings.TrimPrefix(r.URL.Path, imdsMetadataTagsBaseURL+"/")
	if keyPath == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key, err := url.PathUnescape(keyPath)
	if err != nil || key == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	value, ok := imdsTags(info.ID)[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(value))
}

func resolveMetadataRequest(w http.ResponseWriter, r *http.Request, cli *client.Client) (*container.InspectResponse, bool) {
	ip := imdsClientIP(r)
	if ip == "" {
		w.WriteHeader(http.StatusNotFound)
		return nil, false
	}
	info, err := findInstanceByIP(r.Context(), cli, ip)
	if err != nil {
		if errors.Is(err, errIMDSInstanceNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return nil, false
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil, false
	}
	if info.Config == nil {
		w.WriteHeader(http.StatusNotFound)
		return nil, false
	}
	if !imdsEnabled(info.ID) {
		w.WriteHeader(http.StatusNotFound)
		return nil, false
	}
	if !hasValidIMDSToken(r, info.ID) {
		w.WriteHeader(http.StatusUnauthorized)
		return nil, false
	}
	return info, true
}

func handleIMDSToken(w http.ResponseWriter, r *http.Request, cli *client.Client) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ip := imdsClientIP(r)
	if ip == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	info, err := findInstanceByIP(r.Context(), cli, ip)
	if err != nil {
		if errors.Is(err, errIMDSInstanceNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !imdsEnabled(info.ID) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ttlSeconds, err := imdsTokenTTL(r.Header.Get(imdsTokenTTLHeader))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := issueIMDSToken(info.ID, ttlSeconds)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set(imdsTokenTTLHeader, strconv.Itoa(ttlSeconds))
	_, _ = w.Write([]byte(token))
}

func handleIMDSInternalHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func handleIMDSInternalProxyPort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	port := getIMDSProxyPort()
	if port == 0 {
		http.Error(w, "IMDS proxy port is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(strconv.Itoa(port)))
}

func handleIMDSInternalInstanceControl(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, imdsInternalPathPrefix)
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	containerID := parts[0]
	action := parts[1]

	switch action {
	case "enabled":
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
		setIMDSEnabledLocal(containerID, payload.Enabled)
		if !payload.Enabled {
			revokeIMDSTokensLocal(containerID)
		}
		w.WriteHeader(http.StatusNoContent)
	case "tokens":
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		revokeIMDSTokensLocal(containerID)
		w.WriteHeader(http.StatusNoContent)
	case "tags":
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		tags := make(map[string]string)
		if err := json.NewDecoder(r.Body).Decode(&tags); err != nil {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
		setIMDSTagsLocal(containerID, tags)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func imdsClientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	return strings.TrimSpace(host)
}

func findInstanceByIP(ctx context.Context, cli *client.Client, ip string) (*container.InspectResponse, error) {
	args := filters.NewArgs(filters.Arg("label", docker.LabelDC2Enabled+"=true"))
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return nil, fmt.Errorf("listing instance containers: %w", err)
	}
	for _, c := range containers {
		info, err := cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("inspecting container %s: %w", c.ID, err)
		}
		if info.NetworkSettings == nil {
			continue
		}
		for _, n := range info.NetworkSettings.Networks {
			if n != nil && n.IPAddress == ip {
				return &info, nil
			}
		}
	}
	return nil, errIMDSInstanceNotFound
}

func setIMDSEnabled(containerID string, enabled bool) error {
	return imdsControlJSON(
		context.Background(),
		http.MethodPut,
		imdsInternalPathPrefix+containerID+"/enabled",
		struct {
			Enabled bool `json:"enabled"`
		}{Enabled: enabled},
	)
}

func setIMDSEnabledLocal(containerID string, enabled bool) {
	if enabled {
		imdsDisabledInstances.Delete(containerID)
		return
	}
	imdsDisabledInstances.Store(containerID, struct{}{})
}

func imdsEnabled(containerID string) bool {
	_, disabled := imdsDisabledInstances.Load(containerID)
	return !disabled
}

func hasValidIMDSToken(r *http.Request, containerID string) bool {
	token := strings.TrimSpace(r.Header.Get(imdsTokenHeader))
	if token == "" {
		return false
	}
	return validateIMDSToken(token, containerID)
}

func validateIMDSToken(token string, containerID string) bool {
	v, ok := imdsTokens.Load(token)
	if !ok {
		return false
	}
	storedToken, ok := v.(imdsToken)
	if !ok {
		imdsTokens.Delete(token)
		return false
	}
	if storedToken.containerID != containerID {
		return false
	}
	if !storedToken.expiresAt.After(time.Now()) {
		imdsTokens.Delete(token)
		return false
	}
	return true
}

func issueIMDSToken(containerID string, ttlSeconds int) (string, error) {
	tokenBytes := make([]byte, imdsTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generating IMDS token bytes: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	imdsTokens.Store(token, imdsToken{
		containerID: containerID,
		expiresAt:   time.Now().Add(time.Duration(ttlSeconds) * time.Second),
	})
	return token, nil
}

func imdsTokenTTL(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%s header is required", imdsTokenTTLHeader)
	}
	ttlSeconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", imdsTokenTTLHeader)
	}
	if ttlSeconds < imdsTokenMinTTLSeconds || ttlSeconds > imdsTokenMaxTTLSeconds {
		return 0, fmt.Errorf("%s must be between %d and %d", imdsTokenTTLHeader, imdsTokenMinTTLSeconds, imdsTokenMaxTTLSeconds)
	}
	return ttlSeconds, nil
}

func revokeIMDSTokens(containerID string) error {
	return imdsControlNoBody(
		context.Background(),
		http.MethodDelete,
		imdsInternalPathPrefix+containerID+"/tokens",
	)
}

func revokeIMDSTokensLocal(containerID string) {
	imdsTokens.Range(func(k, v any) bool {
		token, ok := v.(imdsToken)
		if ok && token.containerID == containerID {
			imdsTokens.Delete(k)
		}
		return true
	})
}

func setIMDSTags(containerID string, tags map[string]string) error {
	if tags == nil {
		tags = map[string]string{}
	}
	return imdsControlJSON(
		context.Background(),
		http.MethodPut,
		imdsInternalPathPrefix+containerID+"/tags",
		tags,
	)
}

func setIMDSTagsLocal(containerID string, tags map[string]string) {
	if len(tags) == 0 {
		imdsInstanceTags.Delete(containerID)
		return
	}
	copyTags := make(map[string]string, len(tags))
	for k, v := range tags {
		copyTags[k] = v
	}
	imdsInstanceTags.Store(containerID, copyTags)
}

func imdsTags(containerID string) map[string]string {
	v, ok := imdsInstanceTags.Load(containerID)
	if !ok {
		return map[string]string{}
	}
	tags, ok := v.(map[string]string)
	if !ok {
		imdsInstanceTags.Delete(containerID)
		return map[string]string{}
	}
	copyTags := make(map[string]string, len(tags))
	for k, v := range tags {
		copyTags[k] = v
	}
	return copyTags
}

func setIMDSProxyPort(port int) {
	imdsProxyPortMu.Lock()
	defer imdsProxyPortMu.Unlock()
	imdsProxyPort = port
}

func getIMDSProxyPort() int {
	imdsProxyPortMu.RLock()
	defer imdsProxyPortMu.RUnlock()
	return imdsProxyPort
}

func writeIMDSProxyPort(port int) error {
	if port <= 0 {
		return fmt.Errorf("invalid IMDS proxy port %d", port)
	}
	if err := os.WriteFile(imdsPortFilePath, []byte(strconv.Itoa(port)), 0o644); err != nil {
		return err
	}
	return nil
}

func imdsProxyPortFromBackend(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://imds"+imdsInternalProxyPort, nil)
	if err != nil {
		return 0, fmt.Errorf("creating IMDS proxy-port request: %w", err)
	}
	resp, err := imdsControlClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("sending IMDS proxy-port request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("IMDS proxy-port request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32))
	if err != nil {
		return 0, fmt.Errorf("reading IMDS proxy-port response: %w", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port <= 0 {
		return 0, fmt.Errorf("invalid IMDS proxy-port response %q", strings.TrimSpace(string(data)))
	}
	return port, nil
}

func imdsControlJSON(ctx context.Context, method string, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling IMDS control payload: %w", err)
	}
	return imdsControlRequest(ctx, method, path, strings.NewReader(string(body)), "application/json")
}

func imdsControlNoBody(ctx context.Context, method string, path string) error {
	return imdsControlRequest(ctx, method, path, nil, "")
}

func imdsControlRequest(ctx context.Context, method string, path string, body io.Reader, contentType string) error {
	if err := ensureIMDSServer(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://imds"+path, body)
	if err != nil {
		return fmt.Errorf("creating IMDS control request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := imdsControlClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending IMDS control request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf(
		"IMDS control request %s %s failed with status %d: %s",
		method,
		path,
		resp.StatusCode,
		strings.TrimSpace(string(data)),
	)
}
