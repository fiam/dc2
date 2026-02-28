package docker

import (
	"errors"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
)

func TestPreferredContainerNetwork(t *testing.T) {
	t.Parallel()

	t.Run("prefers non-default network", func(t *testing.T) {
		t.Parallel()

		selected := preferredContainerNetwork(
			[]string{"bridge", "project_default", "dc2-imds"},
			"dc2-imds",
		)

		assert.Equal(t, "project_default", selected)
	})

	t.Run("falls back to bridge when only bridge is available", func(t *testing.T) {
		t.Parallel()

		selected := preferredContainerNetwork(
			[]string{"bridge", "dc2-imds"},
			"dc2-imds",
		)

		assert.Equal(t, "bridge", selected)
	})

	t.Run("ignores host and none network modes", func(t *testing.T) {
		t.Parallel()

		selected := preferredContainerNetwork(
			[]string{"host", "none", "dc2-imds"},
			"dc2-imds",
		)

		assert.Empty(t, selected)
	})

	t.Run("is deterministic for multiple non-default candidates", func(t *testing.T) {
		t.Parallel()

		selected := preferredContainerNetwork(
			[]string{"zeta", "alpha", "bridge"},
			"dc2-imds",
		)

		assert.Equal(t, "alpha", selected)
	})
}

func TestAuxiliaryResourcePrefixFromContainerName(t *testing.T) {
	t.Parallel()

	t.Run("defaults for empty name", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, mainResourceNamePrefix, auxiliaryResourcePrefixFromContainerName(""))
	})

	t.Run("trims docker inspect leading slash", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "project-dc2-1", auxiliaryResourcePrefixFromContainerName("/project-dc2-1"))
	})

	t.Run("normalizes invalid characters", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "dc2-prod-api", auxiliaryResourcePrefixFromContainerName("dc2 prod/api"))
	})

	t.Run("enforces max length", func(t *testing.T) {
		t.Parallel()
		value := auxiliaryResourcePrefixFromContainerName("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		assert.Len(t, value, maxAuxResourcePrefixLength)
	})
}

func TestShouldLogIMDSProbeFailure(t *testing.T) {
	t.Parallel()

	assert.True(t, shouldLogIMDSProbeFailure(1))
	assert.True(t, shouldLogIMDSProbeFailure(2))
	assert.True(t, shouldLogIMDSProbeFailure(3))
	assert.False(t, shouldLogIMDSProbeFailure(4))
	assert.False(t, shouldLogIMDSProbeFailure(9))
	assert.True(t, shouldLogIMDSProbeFailure(10))
}

func TestShortenContainerID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "short-id", shortenContainerID("short-id"))
	assert.Equal(t, "123456789012", shortenContainerID("12345678901234567890"))
}

func TestProbeOutputHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "hello", truncateMultiline("hello", 10))
	assert.Equal(t, "hell\n...<truncated>", truncateMultiline("hello", 4))

	assert.Equal(t, "<empty>", printableProbeOutput(" \n\t ", 10))
	assert.Equal(t, "ok", printableProbeOutput("ok", 10))

	details := lastProbeOutputDetails("stdout-value", "stderr-value")
	assert.Contains(t, details, "last_probe_stdout=stdout-value")
	assert.Contains(t, details, "last_probe_stderr=stderr-value")
	assert.Contains(t, details, "\n")
}

func TestIsIMDSProxyEnsureTransientError(t *testing.T) {
	t.Parallel()

	assert.False(t, isIMDSProxyEnsureTransientError(nil))
	assert.True(t, isIMDSProxyEnsureTransientError(cerrdefs.ErrNotFound))
	assert.True(t, isIMDSProxyEnsureTransientError(errors.New("No such container: abc")))
	assert.True(t, isIMDSProxyEnsureTransientError(errors.New("container xyz is not found")))
	assert.True(t, isIMDSProxyEnsureTransientError(errors.New("container abc is marked for removal")))
	assert.False(t, isIMDSProxyEnsureTransientError(errors.New("not found")))
	assert.False(t, isIMDSProxyEnsureTransientError(errors.New("boom")))
}
