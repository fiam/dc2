package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/lmittmann/tint"

	"github.com/fiam/dc2/pkg/dc2"
)

func main() {
	logLevel := slog.LevelInfo

	if level := os.Getenv("LOG_LEVEL"); level != "" {
		var err error
		logLevel, err = parseLogLevel(level)
		if err != nil {
			log.Fatal(err)
		}
	}

	slog.SetDefault(slog.New(
		tint.NewHandler(os.Stdout, &tint.Options{
			Level:      logLevel,
			TimeFormat: time.Kitchen,
		}),
	))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "0.0.0.0:8080"
	}

	slog.Debug("starting server", slog.String("addr", addr))

	srv, err := dc2.NewServer(addr)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()

	stop()
	slog.Debug("shutting down gracefully, press Ctrl+C again to force")

	// Perform application shutdown with a maximum timeout of 5 seconds.
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		if err := srv.Shutdown(timeoutCtx); err != nil {
			log.Fatalln(err)
		}
	}()

	select {
	case <-closed:
		slog.Info("shutdown completed")
		os.Exit(0)
	case <-timeoutCtx.Done():
		if timeoutCtx.Err() == context.DeadlineExceeded {
			log.Fatal("timeout exceeded, forcing shutdown")
		}
	}
}

func parseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", level)
}
