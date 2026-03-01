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
	mux.HandleFunc("/_dc2/test-profile", srv.serveTestProfile)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := api.ContextWithRequestID(r.Context(), requestID)
		ctx = api.ContextWithAction(ctx, r.FormValue("Action"))
		r = r.WithContext(ctx)
		req, err := srv.format.DecodeRequest(r)
		if err != nil {
			if err := srv.format.EncodeError(ctx, w, err); err != nil {
				api.Logger(ctx).Error("serving decoding error to client", slog.Any("error", err))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
			return
		}
		resp, err := srv.dispatch.Dispatch(ctx, req)
		if err != nil {
			if err := srv.format.EncodeError(ctx, w, err); err != nil {
				api.Logger(ctx).Error("serving error to client", slog.Any("error", err))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		} else {
			if err := srv.format.EncodeResponse(ctx, w, resp); err != nil {
				api.Logger(ctx).Error("serving response to client", slog.Any("error", err))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}
	})
	return srv, nil
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
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Region returns the region identifier that the server is emulating (e.g. us-east-1)
func (s *Server) Region() string {
	return s.opts.Region
}

func (s *Server) ListenAndServe() error {
	return s.server.ListenAndServe()
}

func (s *Server) Serve(listener net.Listener) error {
	return s.server.Serve(listener)
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
