package dc2

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

type Server struct {
	server   *http.Server
	dispatch *Dispatcher
	opts     options
}

func NewServer(addr string, opts ...Option) (*Server, error) {
	exec, err := NewDispatcher()
	if err != nil {
		return nil, fmt.Errorf("initializing dispatcher: %w", err)
	}

	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	mux := http.NewServeMux()
	httpServer := &http.Server{
		Handler: mux,
		Addr:    addr,
	}

	srv := &Server{
		server:   httpServer,
		dispatch: exec,
		opts:     o,
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		logger := slog.With("request_id", requestID)
		resp, err := srv.handleRequest(logger, r)
		if err != nil {
			var awsError *AWSError
			if errors.As(err, &awsError) {
				srv.serveAWSError(r.Context(), w, logger, requestID, awsError)
			} else {
				logger.Error("unexpected error", slog.Any("error", err))
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		} else {
			data, err := xml.MarshalIndent(resp, "", "  ")
			if err != nil {
				logger.Error("serializing XML", slog.Any("error", err))
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if logger.Enabled(r.Context(), slog.LevelDebug) {
				log.Printf("response:\n%s\n", string(data))
			}

			w.Header().Set("Content-Type", "application/xml")
			if _, err := w.Write(data); err != nil {
				logger.Warn("writing error response to client", slog.Any("error", err))
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

func (s *Server) serveAWSError(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, requestID string, e *AWSError) {
	code := e.Code
	statusCode := http.StatusBadRequest
	switch code {
	case ErrorCodeMethodNotAllowed:
		statusCode = http.StatusMethodNotAllowed
	case "":
		// Unknown error
		statusCode = http.StatusInternalServerError
	}
	errorResponse := xmlErrorResponse{
		Errors: xmlErrors{
			Error: xmlError{
				Code:    code,
				Message: e.Error(),
			},
		},
		RequestID: requestID,
	}

	// Serialize the response to XML
	xmlData, err := xml.MarshalIndent(errorResponse, "", "  ")
	if err != nil {
		logger.Error("serializing XML", slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(statusCode)
	if _, err := w.Write(xmlData); err != nil {
		logger.Warn("writing error response to client", slog.Any("error", err))
	}
	if logger.Enabled(ctx, slog.LevelDebug) {
		log.Printf("returning error with status code %d:\n%s\n", statusCode, string(xmlData))
	}
}

func (s *Server) handleRequest(logger *slog.Logger, r *http.Request) (Response, error) {
	if r.Method != http.MethodPost {
		return nil, ErrWithCode(ErrorCodeMethodNotAllowed, nil)
	}
	if err := r.ParseForm(); err != nil {
		return nil, ErrWithCode(ErrorCodeInvalidForm, err)
	}
	if logger.Enabled(r.Context(), slog.LevelDebug) {
		log.Printf("received request %s %s %+v\n", r.Method, r.URL.Path, r.Form)
	}
	req, err := parseRequest(r)
	if err != nil {
		return nil, err
	}
	return s.dispatch.Exec(r.Context(), req)
}
