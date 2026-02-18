package dc2

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	defaultInstanceShutdownDuration    = 5 * time.Second
	defaultInstanceTerminationDuration = 3 * time.Second
	defaultRegion                      = "us-east-1"
)

type ExitResourceMode string

const (
	ExitResourceModeCleanup ExitResourceMode = "cleanup"
	ExitResourceModeKeep    ExitResourceMode = "keep"
	ExitResourceModeAssert  ExitResourceMode = "assert"
)

func ParseExitResourceMode(raw string) (ExitResourceMode, error) {
	mode := ExitResourceMode(strings.ToLower(strings.TrimSpace(raw)))
	switch mode {
	case ExitResourceModeCleanup, ExitResourceModeKeep, ExitResourceModeAssert:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid exit resource mode %q", raw)
	}
}

type options struct {
	// InstanceShutdownDuration indicates how long an instance takes to transition from shutting-down to terminated
	InstanceShutdownDuration time.Duration
	// InstanceTerminationDuration indicates how long an instance stays around after being terminated
	InstanceTerminationDuration time.Duration
	InstanceNetwork             string
	ExitResourceMode            ExitResourceMode
	Region                      string
	Logger                      *slog.Logger
}

func defaultOptions() options {
	return options{
		InstanceShutdownDuration:    defaultInstanceShutdownDuration,
		InstanceTerminationDuration: defaultInstanceTerminationDuration,
		ExitResourceMode:            ExitResourceModeCleanup,
	}
}

type Option func(opt *options)

func WithRegion(region string) Option {
	return func(opt *options) {
		opt.Region = region
	}
}

// WithInstanceNetwork sets the instance data-plane network used for workload
// traffic. When empty, dc2 auto-detects the current container network (when
// running in Docker) and otherwise falls back to Docker's default bridge
// network.
func WithInstanceNetwork(name string) Option {
	return func(opt *options) {
		opt.InstanceNetwork = name
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(opt *options) {
		opt.Logger = logger
	}
}

// WithExitResourceMode sets shutdown behavior for owned resources.
func WithExitResourceMode(mode ExitResourceMode) Option {
	return func(opt *options) {
		opt.ExitResourceMode = mode
	}
}

// WithInstanceShutdownDuration sets how long an instance takes to transition from shutting-down to terminated
func WithInstanceShutdownDuration(duration time.Duration) Option {
	return func(opt *options) {
		opt.InstanceShutdownDuration = duration
	}
}

// WithInstanceTerminationDuration sets how long an instance stays around until
func WithInstanceTerminationDuration(duration time.Duration) Option {
	return func(opt *options) {
		opt.InstanceTerminationDuration = duration
	}
}
