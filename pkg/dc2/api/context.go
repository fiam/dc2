package api

import (
	"context"
	"log/slog"
)

type contextKey string

const (
	loggerContextKey    = contextKey("logger")
	requestIDContextKey = contextKey("request_id")
	actionContextKey    = contextKey("action")
)

func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
}

func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey, requestID)
}

func ContextWithAction(ctx context.Context, action string) context.Context {
	return context.WithValue(ctx, actionContextKey, action)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)
	return id
}

func RequestAction(ctx context.Context) string {
	action, _ := ctx.Value(actionContextKey).(string)
	return action
}

func Logger(ctx context.Context) *slog.Logger {
	logger, _ := ctx.Value(loggerContextKey).(*slog.Logger)
	if logger == nil {
		logger = slog.Default()
	}
	id := RequestID(ctx)
	if id != "" {
		logger = logger.With(slog.String("request_id", id))
	}
	return logger
}
