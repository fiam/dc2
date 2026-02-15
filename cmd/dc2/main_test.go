package main

import (
	"bytes"
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"

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
