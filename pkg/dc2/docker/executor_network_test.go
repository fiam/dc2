package docker

import (
	"testing"

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
