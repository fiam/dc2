package dc2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/google/uuid"

	"github.com/fiam/dc2/pkg/dc2/api"
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

	imds, err := newIMDSController()
	if err != nil {
		return nil, fmt.Errorf("initializing IMDS server: %w", err)
	}

	dispatcherOpts := DispatcherOptions{
		Region:          region,
		IMDSBackendPort: imds.BackendPort(),
		InstanceNetwork: o.InstanceNetwork,
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := api.ContextWithRequestID(r.Context(), requestID)
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
