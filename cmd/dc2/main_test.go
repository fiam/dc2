package main

import (
	"bytes"
	"flag"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/buildinfo"
)

func TestFormatVersionLine(t *testing.T) {
	t.Parallel()

	line := formatVersionLine("dc2", buildinfo.Info{
		Version:    "v1.2.3",
		Commit:     "abc123",
		CommitTime: "2026-02-15T00:00:00Z",
		Dirty:      true,
		GoVersion:  "go1.26.0",
	})
	assert.Equal(
		t,
		"dc2 version=v1.2.3 commit=abc123 dirty=true commit_time=2026-02-15T00:00:00Z go=go1.26.0",
		line,
	)
}

func TestConfigureUsageIncludesBuildInfo(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	fs := flag.NewFlagSet("dc2", flag.ContinueOnError)
	fs.String("addr", "", "Address to listen on")

	configureUsage(fs, &out, "dc2", buildinfo.Info{
		Version: "v1.2.3",
		Commit:  "abc123",
		Dirty:   true,
	})
	fs.Usage()

	usage := out.String()
	assert.Contains(t, usage, "Usage:\n  dc2 [flags]")
	assert.Contains(t, usage, "Build:\n  dc2 version=v1.2.3 commit=abc123 dirty=true")
	assert.Contains(t, usage, "-addr string")
}

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: "INFO", want: slog.LevelInfo},
		{name: "warn", input: "Warn", want: slog.LevelWarn},
		{name: "error", input: "ERROR", want: slog.LevelError},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseLogLevel(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, err := parseLogLevel("trace")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown log level "trace"`)
}

func TestParseOptionalDuration(t *testing.T) {
	const envKey = "DC2_TEST_PARSE_OPTIONAL_DURATION"

	t.Run("returns zero when empty", func(t *testing.T) {
		t.Parallel()

		got, err := parseOptionalDuration("", envKey)
		require.NoError(t, err)
		assert.Equal(t, time.Duration(0), got)
	})

	t.Run("flag value overrides env", func(t *testing.T) {
		t.Setenv(envKey, "30s")

		got, err := parseOptionalDuration("5s", envKey)
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, got)
	})

	t.Run("uses env when flag is empty", func(t *testing.T) {
		t.Setenv(envKey, "2m")

		got, err := parseOptionalDuration("", envKey)
		require.NoError(t, err)
		assert.Equal(t, 2*time.Minute, got)
	})

	t.Run("returns parse error for invalid input", func(t *testing.T) {
		t.Parallel()

		_, err := parseOptionalDuration("bad-duration", envKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid duration for "+envKey)
	})

	t.Run("returns parse error for invalid env", func(t *testing.T) {
		t.Setenv(envKey, "still-bad")

		_, err := parseOptionalDuration("", envKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid duration for "+envKey)
	})
}
