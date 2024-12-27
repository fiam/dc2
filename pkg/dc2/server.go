package dc2

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/format"
	"github.com/google/uuid"
)

type Server struct {
	server   *http.Server
	format   format.Format
	dispatch *Dispatcher
	opts     options
}

func NewServer(addr string, opts ...Option) (*Server, error) {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	dispatch, err := NewDispatcher()
	if err != nil {
		return nil, fmt.Errorf("initializing dispatcher: %w", err)
	}

	mux := http.NewServeMux()
	httpServer := &http.Server{
		Handler: mux,
		Addr:    addr,
	}

	srv := &Server{
		server:   httpServer,
		format:   &format.XML{},
		dispatch: dispatch,
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

func (s *Server) ListenAndServe() error {
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
