package api

import (
	"context"
	"log/slog"
)

type contextKey string

const (
	requestIDContextKey = contextKey("request_id")
)

func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey, requestID)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)
	return id
}

func Logger(ctx context.Context) *slog.Logger {
	logger := slog.Default()
	id := RequestID(ctx)
	if id != "" {
		logger = logger.With(slog.String("request_id", id))
	}
	return logger
}
