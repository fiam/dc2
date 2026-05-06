package dc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"

	"github.com/google/uuid"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/buildinfo"
	"github.com/fiam/dc2/pkg/dc2/format"
)

type Server struct {
	server   *http.Server
	format   format.Format
	dispatch *Dispatcher
	imds     *imdsController
	opts     options
}

func NewServer(addr string, opts ...Option) (*Server, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	region := o.Region
	if region == "" {
		region = defaultRegion
	}
	o.Region = region
	exitResourceMode, err := ParseExitResourceMode(string(o.ExitResourceMode))
	if err != nil {
		return nil, err
	}
	o.ExitResourceMode = exitResourceMode

	imds, err := newIMDSController()
	if err != nil {
		return nil, fmt.Errorf("initializing IMDS server: %w", err)
	}

	dispatcherOpts := DispatcherOptions{
		Region:            region,
		IMDSBackendPort:   imds.BackendPort(),
		InstanceNetwork:   o.InstanceNetwork,
		TestProfileInput:  o.TestProfileInput,
		SpotReclaimAfter:  o.SpotReclaimAfter,
		SpotReclaimNotice: o.SpotReclaimNotice,
		ExitResourceMode:  o.ExitResourceMode,
	}
	dispatch, err := NewDispatcher(context.Background(), dispatcherOpts, imds)
	if err != nil {
		_ = imds.Close(context.Background())
		return nil, fmt.Errorf("initializing dispatcher: %w", err)
	}

	var baseContext func(l net.Listener) context.Context

	if o.Logger != nil {
		ctx := api.ContextWithLogger(context.Background(), o.Logger)
		baseContext = func(net.Listener) context.Context {
			return ctx
		}
	}

	mux := http.NewServeMux()
	httpServer := &http.Server{
		Handler:     mux,
		Addr:        addr,
		BaseContext: baseContext,
	}

	srv := &Server{
		server:   httpServer,
		format:   &format.XML{},
		dispatch: dispatch,
		imds:     imds,
		opts:     o,
	}
	mux.HandleFunc("/_dc2/metadata", srv.serveMetadata)
	mux.HandleFunc("/_dc2/ready", srv.serveReady)
	mux.HandleFunc("/_dc2/test-profile", srv.serveTestProfile)
	mux.HandleFunc("/", srv.serveAWS)
	return srv, nil
}

func (s *Server) serveAWS(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.New().String()
	ctx := api.ContextWithRequestID(r.Context(), requestID)
	action := ""
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		attrs := []slog.Attr{
			slog.Any("panic", recovered),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("stack", string(debug.Stack())),
		}
		if action != "" {
			attrs = append(attrs, slog.String("action", action))
		}
		api.Logger(ctx).LogAttrs(ctx, slog.LevelError, "panic serving AWS request", attrs...)
		http.Error(w, fmt.Sprintf("Internal Server Error: %v", recovered), http.StatusInternalServerError)
	}()

	action = r.FormValue("Action")
	ctx = api.ContextWithAction(ctx, action)
	r = r.WithContext(ctx)
	req, err := s.format.DecodeRequest(r)
	if err != nil {
		if err := s.format.EncodeError(ctx, w, err); err != nil {
			api.Logger(ctx).Error("serving decoding error to client", slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}
	resp, err := s.dispatch.Dispatch(ctx, req)
	if err != nil {
		if err := s.format.EncodeError(ctx, w, err); err != nil {
			api.Logger(ctx).Error("serving error to client", slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	} else {
		if err := s.format.EncodeResponse(ctx, w, resp); err != nil {
			api.Logger(ctx).Error("serving response to client", slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}

func (s *Server) serveMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := struct {
		Name   string         `json:"name"`
		Region string         `json:"region"`
		Build  buildinfo.Info `json:"build"`
	}{
		Name:   "dc2",
		Region: s.opts.Region,
		Build:  buildinfo.Current(),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.Logger(r.Context()).Error("serving metadata response", slog.Any("error", err))
	}
}

func (s *Server) serveReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := struct {
		Name              string           `json:"name"`
		Ready             bool             `json:"ready"`
		Region            string           `json:"region"`
		Build             buildinfo.Info   `json:"build"`
		ConfiguredNetwork string           `json:"configured_instance_network,omitempty"`
		ExitResourceMode  ExitResourceMode `json:"exit_resource_mode"`
		IMDSBackendPort   int              `json:"imds_backend_port"`
	}{
		Name:              "dc2",
		Ready:             true,
		Region:            s.opts.Region,
		Build:             buildinfo.Current(),
		ConfiguredNetwork: s.opts.InstanceNetwork,
		ExitResourceMode:  s.opts.ExitResourceMode,
		IMDSBackendPort:   s.imdsBackendPort(),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		api.Logger(r.Context()).Error("serving readiness response", slog.Any("error", err))
	}
}

func (s *Server) serveTestProfile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		yaml, ok := s.dispatch.currentTestProfileYAML()
		if !ok {
			http.Error(w, "no active test profile", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		if _, err := w.Write([]byte(yaml)); err != nil {
			api.Logger(r.Context()).Error("serving test profile response", slog.Any("error", err))
		}
	case http.MethodDelete:
		s.dispatch.clearTestProfile()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("reading request body: %v", err), http.StatusBadRequest)
			return
		}
		if err := s.dispatch.updateTestProfileFromYAML(string(body)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		currentYAML, ok := s.dispatch.currentTestProfileYAML()
		if !ok {
			http.Error(w, "no active test profile", http.StatusNotFound)
			return
		}
		patchBody, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("reading request body: %v", err), http.StatusBadRequest)
			return
		}
		mergedYAML, err := mergeTestProfileYAML(currentYAML, string(patchBody))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.dispatch.updateTestProfileFromYAML(mergedYAML); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Region returns the region identifier that the server is emulating (e.g. us-east-1)
func (s *Server) Region() string {
	return s.opts.Region
}

func (s *Server) ListenAndServe() error {
	addr := s.server.Addr
	if addr == "" {
		addr = ":http"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		s.logServerEvent(
			slog.LevelError,
			"dc2 API server listen failed",
			slog.String("addr", addr),
			slog.Any("error", err),
		)
		return err
	}
	return s.Serve(listener)
}

func (s *Server) Serve(listener net.Listener) error {
	addr := listener.Addr().String()
	s.logServerEvent(slog.LevelInfo, "dc2 API server listening", slog.String("addr", addr))
	err := s.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		s.logServerEvent(slog.LevelInfo, "dc2 API server stopped", slog.String("addr", addr))
		return err
	}
	if err != nil {
		s.logServerEvent(slog.LevelError, "dc2 API server failed", slog.String("addr", addr), slog.Any("error", err))
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	var shutdownErr error
	if err := s.dispatch.Close(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("closing dispatcher: %w", err))
	}
	if s.imds != nil {
		if err := s.imds.Close(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("closing IMDS server: %w", err))
		}
	}
	if err := s.server.Shutdown(ctx); err != nil {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	return shutdownErr
}

func (s *Server) logServerEvent(level slog.Level, message string, attrs ...slog.Attr) {
	allAttrs := append(s.serverLogAttrs(), attrs...)
	s.logger().LogAttrs(context.Background(), level, message, allAttrs...)
}

func (s *Server) serverLogAttrs() []slog.Attr {
	build := buildinfo.Current()
	attrs := []slog.Attr{
		slog.String("region", s.opts.Region),
		slog.String("version", build.Version),
		slog.String("exit_resource_mode", string(s.opts.ExitResourceMode)),
		slog.Int("imds_backend_port", s.imdsBackendPort()),
		slog.Bool("test_profile_configured", s.opts.TestProfileInput != ""),
	}
	if build.Commit != "" {
		attrs = append(attrs, slog.String("commit", build.Commit))
	}
	if build.CommitTime != "" {
		attrs = append(attrs, slog.String("commit_time", build.CommitTime))
	}
	if build.Dirty {
		attrs = append(attrs, slog.Bool("dirty", true))
	}
	if build.GoVersion != "" {
		attrs = append(attrs, slog.String("go_version", build.GoVersion))
	}
	if s.opts.InstanceNetwork != "" {
		attrs = append(attrs, slog.String("configured_instance_network", s.opts.InstanceNetwork))
	}
	return attrs
}

func (s *Server) logger() *slog.Logger {
	if s.opts.Logger != nil {
		return s.opts.Logger
	}
	return slog.Default()
}

func (s *Server) imdsBackendPort() int {
	if s.imds == nil {
		return 0
	}
	return s.imds.BackendPort()
}
