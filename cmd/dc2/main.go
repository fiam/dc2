package main

import (
	"context"
	"errors"
	"flag"
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

var Version = "dev"

var (
	version = flag.Bool("version", false, "Display version and exit")
	level   = flag.String("log-level", "", "Log level")
	addr    = flag.String("addr", "", "Address to listen on")
)

func main() {
	flag.Parse()

	if *version {
		fmt.Fprintf(os.Stderr, "%s %s\n", os.Args[0], Version)
		os.Exit(0)
	}

	logLevel := slog.LevelInfo

	levelStr := *level
	if levelStr == "" {
		levelStr = os.Getenv("LOG_LEVEL")
	}
	if levelStr != "" {
		var err error
		logLevel, err = parseLogLevel(levelStr)
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

	listenAddr := *addr
	if listenAddr == "" {
		listenAddr = os.Getenv("ADDR")
	}
	if listenAddr == "" {
		listenAddr = "0.0.0.0:8080"
	}

	slog.Debug("starting server", slog.String("addr", listenAddr))

	srv, err := dc2.NewServer(listenAddr)
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
