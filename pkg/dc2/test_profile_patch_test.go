package dc2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeTestProfileYAML(t *testing.T) {
	t.Parallel()

	current := `
version: 1
rules:
  - name: freeze
    when:
      action: RunInstances
    delay:
      before:
        allocate: 1h
`
	patch := `
rules:
  - name: freeze
    when:
      action: RunInstances
    delay:
      before:
        allocate: 30s
`
	merged, err := mergeTestProfileYAML(current, patch)
	require.NoError(t, err)
	assert.Contains(t, merged, "allocate: 30s")
	assert.NotContains(t, merged, "allocate: 1h")
	assert.Contains(t, merged, "version: 1")
}

func TestMergeTestProfileYAMLDeleteField(t *testing.T) {
	t.Parallel()

	current := `
version: 1
rules:
  - name: freeze
`
	patch := `
rules: null
`
	merged, err := mergeTestProfileYAML(current, patch)
	require.NoError(t, err)
	assert.Contains(t, merged, "version: 1")
	assert.NotContains(t, merged, "rules:")
}

func TestMergeTestProfileYAMLRejectsEmptyPatch(t *testing.T) {
	t.Parallel()

	_, err := mergeTestProfileYAML("version: 1\nrules: []", "  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "patch YAML must define at least one field")
}
