package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lmittmann/tint"

	"github.com/fiam/dc2/pkg/dc2"
	"github.com/fiam/dc2/pkg/dc2/buildinfo"
)

var (
	version          = flag.Bool("version", false, "Display version and exit")
	level            = flag.String("log-level", "", "Log level")
	addr             = flag.String("addr", "", "Address to listen on")
	instanceNetwork  = flag.String("instance-network", "", "Instance workload network name (optional)")
	exitResourceMode = flag.String("exit-resource-mode", "", "Exit resource mode: cleanup|keep|assert")
)

func main() {
	build := buildinfo.Current()
	binary := filepath.Base(os.Args[0])
	configureUsage(flag.CommandLine, os.Stderr, binary, build)

	flag.Parse()

	if *version {
		fmt.Fprintln(os.Stdout, formatVersionLine(binary, build))
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listenAddr := *addr
	if listenAddr == "" {
		listenAddr = os.Getenv("ADDR")
	}
	if listenAddr == "" {
		listenAddr = "0.0.0.0:8080"
	}

	workloadNetwork := strings.TrimSpace(*instanceNetwork)
	if workloadNetwork == "" {
		workloadNetwork = strings.TrimSpace(os.Getenv("INSTANCE_NETWORK"))
	}

	exitModeRaw := strings.TrimSpace(*exitResourceMode)
	if exitModeRaw == "" {
		exitModeRaw = strings.TrimSpace(os.Getenv("DC2_EXIT_RESOURCE_MODE"))
	}
	if exitModeRaw == "" {
		exitModeRaw = string(dc2.ExitResourceModeCleanup)
	}
	exitMode, err := dc2.ParseExitResourceMode(exitModeRaw)
	if err != nil {
		log.Fatal(err)
	}

	slog.Debug(
		"starting server",
		slog.String("addr", listenAddr),
		slog.String("instance_network", workloadNetwork),
		slog.String("exit_resource_mode", string(exitMode)),
	)

	opts := []dc2.Option{}
	if workloadNetwork != "" {
		opts = append(opts, dc2.WithInstanceNetwork(workloadNetwork))
	}
	opts = append(opts, dc2.WithExitResourceMode(exitMode))
	srv, err := dc2.NewServer(listenAddr, opts...)
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

func configureUsage(fs *flag.FlagSet, out io.Writer, binary string, info buildinfo.Info) {
	fs.SetOutput(out)
	fs.Usage = func() {
		fmt.Fprintf(out, "Usage:\n  %s [flags]\n\n", binary)
		fmt.Fprintf(out, "Build:\n  %s\n\n", formatVersionLine(binary, info))
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
}

func formatVersionLine(binary string, info buildinfo.Info) string {
	parts := []string{
		binary,
		"version=" + info.Version,
	}

	if info.Commit != "" {
		parts = append(parts, "commit="+info.Commit)
	}
	if info.Dirty {
		parts = append(parts, "dirty=true")
	}
	if info.CommitTime != "" {
		parts = append(parts, "commit_time="+info.CommitTime)
	}
	if info.GoVersion != "" {
		parts = append(parts, "go="+info.GoVersion)
	}

	return strings.Join(parts, " ")
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
