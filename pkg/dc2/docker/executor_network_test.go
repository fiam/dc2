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
