package dc2

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
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
	imdsBackendAddr = "0.0.0.0:0"

	imdsTokenHeader    = "X-aws-ec2-metadata-token"
	imdsTokenTTLHeader = "X-aws-ec2-metadata-token-ttl-seconds"

	imdsTokenMinTTLSeconds = 1
	imdsTokenMaxTTLSeconds = 21600
	imdsTokenBytes         = 32

	imdsMetadataTagsBaseURL = "/latest/meta-data/tags/instance"
)

var errIMDSInstanceNotFound = errors.New("imds instance not found")

type imdsToken struct {
	containerID string
	expiresAt   time.Time
}

type imdsController struct {
	cli      *client.Client
	server   *http.Server
	listener net.Listener

	disabledInstances sync.Map
	tokens            sync.Map
	instanceTags      sync.Map
}

func newIMDSController() (*imdsController, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}

	listener, err := net.Listen("tcp", imdsBackendAddr)
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("binding IMDS backend listener: %w", err)
	}

	controller := &imdsController{
		cli:      cli,
		listener: listener,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/latest/api/token", controller.handleToken)
	mux.HandleFunc("/latest/meta-data/instance-id", controller.handleInstanceID)
	mux.HandleFunc("/latest/user-data", controller.handleUserData)
	mux.HandleFunc(imdsMetadataTagsBaseURL, controller.handleInstanceTagKeys)
	mux.HandleFunc(imdsMetadataTagsBaseURL+"/", controller.handleInstanceTagValue)

	controller.server = &http.Server{Handler: mux}

	go func() {
		if serveErr := controller.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("IMDS backend server failed", slog.Any("error", serveErr))
		}
	}()

	return controller, nil
}

func (c *imdsController) BackendPort() int {
	tcpAddr, ok := c.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return tcpAddr.Port
}

func (c *imdsController) Close(ctx context.Context) error {
	var closeErr error
	if c.server != nil {
		if err := c.server.Shutdown(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("shutting down IMDS backend server: %w", err))
		}
	}
	if c.cli != nil {
		if err := c.cli.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("closing IMDS Docker client: %w", err))
		}
	}
	return closeErr
}

func (c *imdsController) SetEnabled(containerID string, enabled bool) error {
	if enabled {
		c.disabledInstances.Delete(containerID)
		return nil
	}
	c.disabledInstances.Store(containerID, struct{}{})
	c.revokeTokensLocal(containerID)
	return nil
}

func (c *imdsController) Enabled(containerID string) bool {
	_, disabled := c.disabledInstances.Load(containerID)
	return !disabled
}

func (c *imdsController) RevokeTokens(containerID string) error {
	c.revokeTokensLocal(containerID)
	return nil
}

func (c *imdsController) SetTags(containerID string, tags map[string]string) error {
	if len(tags) == 0 {
		c.instanceTags.Delete(containerID)
		return nil
	}
	copyTags := make(map[string]string, len(tags))
	maps.Copy(copyTags, tags)
	c.instanceTags.Store(containerID, copyTags)
	return nil
}

func (c *imdsController) handleInstanceID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := c.resolveMetadataRequest(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(apiInstanceID(executor.InstanceID(info.ID))))
}

func (c *imdsController) handleUserData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := c.resolveMetadataRequest(w, r)
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

func (c *imdsController) handleInstanceTagKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := c.resolveMetadataRequest(w, r)
	if !ok {
		return
	}
	tags := c.tags(info.ID)
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

func (c *imdsController) handleInstanceTagValue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	info, ok := c.resolveMetadataRequest(w, r)
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
	value, ok := c.tags(info.ID)[key]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(value))
}

func (c *imdsController) resolveMetadataRequest(w http.ResponseWriter, r *http.Request) (*container.InspectResponse, bool) {
	ip := imdsClientIP(r)
	if ip == "" {
		w.WriteHeader(http.StatusNotFound)
		return nil, false
	}
	info, err := c.findInstanceByIP(r.Context(), ip)
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
	if !c.Enabled(info.ID) {
		w.WriteHeader(http.StatusNotFound)
		return nil, false
	}
	if !c.hasValidToken(r, info.ID) {
		w.WriteHeader(http.StatusUnauthorized)
		return nil, false
	}
	return info, true
}

func (c *imdsController) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ip := imdsClientIP(r)
	if ip == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	info, err := c.findInstanceByIP(r.Context(), ip)
	if err != nil {
		if errors.Is(err, errIMDSInstanceNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !c.Enabled(info.ID) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ttlSeconds, err := imdsTokenTTL(r.Header.Get(imdsTokenTTLHeader))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := c.issueToken(info.ID, ttlSeconds)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set(imdsTokenTTLHeader, strconv.Itoa(ttlSeconds))
	_, _ = w.Write([]byte(token))
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

func (c *imdsController) findInstanceByIP(ctx context.Context, ip string) (*container.InspectResponse, error) {
	args := filters.NewArgs(filters.Arg("label", docker.LabelDC2Enabled+"=true"))
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return nil, fmt.Errorf("listing instance containers: %w", err)
	}
	for _, summary := range containers {
		if !summaryHasIP(summary, ip) {
			continue
		}
		info, err := c.cli.ContainerInspect(ctx, summary.ID)
		if err != nil {
			if cerrdefs.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("inspecting container %s: %w", summary.ID, err)
		}
		return &info, nil
	}
	return nil, errIMDSInstanceNotFound
}

func summaryHasIP(summary container.Summary, ip string) bool {
	if summary.NetworkSettings == nil || summary.NetworkSettings.Networks == nil {
		return false
	}
	for _, endpoint := range summary.NetworkSettings.Networks {
		if endpoint != nil && endpoint.IPAddress == ip {
			return true
		}
	}
	return false
}

func (c *imdsController) hasValidToken(r *http.Request, containerID string) bool {
	token := strings.TrimSpace(r.Header.Get(imdsTokenHeader))
	if token == "" {
		return false
	}
	return c.validateToken(token, containerID)
}

func (c *imdsController) validateToken(token string, containerID string) bool {
	v, ok := c.tokens.Load(token)
	if !ok {
		return false
	}
	storedToken, ok := v.(imdsToken)
	if !ok {
		c.tokens.Delete(token)
		return false
	}
	if storedToken.containerID != containerID {
		return false
	}
	if !storedToken.expiresAt.After(time.Now()) {
		c.tokens.Delete(token)
		return false
	}
	return true
}

func (c *imdsController) issueToken(containerID string, ttlSeconds int) (string, error) {
	tokenBytes := make([]byte, imdsTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generating IMDS token bytes: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	c.tokens.Store(token, imdsToken{
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

func (c *imdsController) revokeTokensLocal(containerID string) {
	c.tokens.Range(func(k, v any) bool {
		token, ok := v.(imdsToken)
		if ok && token.containerID == containerID {
			c.tokens.Delete(k)
		}
		return true
	})
}

func (c *imdsController) tags(containerID string) map[string]string {
	v, ok := c.instanceTags.Load(containerID)
	if !ok {
		return map[string]string{}
	}
	tags, ok := v.(map[string]string)
	if !ok {
		c.instanceTags.Delete(containerID)
		return map[string]string{}
	}
	copyTags := make(map[string]string, len(tags))
	maps.Copy(copyTags, tags)
	return copyTags
}
