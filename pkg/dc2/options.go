package dc2

import (
	"log/slog"
	"time"
)

const (
	defaultInstanceShutdownDuration    = 5 * time.Second
	defaultInstanceTerminationDuration = 3 * time.Second
	defaultRegion                      = "us-east-1"
)

type options struct {
	// InstanceShutdownDuration indicates how long an instance takes to transition from shutting-down to terminated
	InstanceShutdownDuration time.Duration
	// InstanceTerminationDuration indicates how long an instance stays around after being terminated
	InstanceTerminationDuration time.Duration
	Region                      string
	Logger                      *slog.Logger
}

func defaultOptions() options {
	return options{
		InstanceShutdownDuration:    defaultInstanceShutdownDuration,
		InstanceTerminationDuration: defaultInstanceTerminationDuration,
	}
}

type Option func(opt *options)

func WithRegion(region string) Option {
	return func(opt *options) {
		opt.Region = region
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(opt *options) {
		opt.Logger = logger
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
