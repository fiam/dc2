package dc2

import "time"

const (
	defaultInstanceShutdownDuration    = 5 * time.Second
	defaultInstanceTerminationDuration = 3 * time.Second
)

type options struct {
	// InstanceShutdownDuration indicates how long an instance takes to transition from shutting-down to terminated
	InstanceShutdownDuration time.Duration
	// InstanceTerminationDuration indicates how long an instance stays around after being terminated
	InstanceTerminationDuration time.Duration
}

func defaultOptions() options {
	return options{
		InstanceShutdownDuration:    defaultInstanceShutdownDuration,
		InstanceTerminationDuration: defaultInstanceTerminationDuration,
	}
}

type Option func(opt *options)

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
