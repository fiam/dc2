package dc2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/testprofile"
)

func TestLoadStartupTestProfileFromYAML(t *testing.T) {
	t.Parallel()

	input := `
version: 1
rules:
  - when:
      action: RunInstances
    delay:
      before:
        start: 200ms
`
	profile, yaml, err := loadStartupTestProfile(input)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, 1, profile.Version)
	assert.Equal(t, "version: 1\nrules:\n  - when:\n      action: RunInstances\n    delay:\n      before:\n        start: 200ms", yaml)
	assert.Equal(t, 200*time.Millisecond, profile.Delay(testprofile.HookBefore, testprofile.PhaseStart, testprofile.MatchInput{
		Action: "RunInstances",
	}))
}

func TestLoadStartupTestProfileFromPath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "profile.yaml")
	err := os.WriteFile(path, []byte(`
version: 1
rules:
  - when:
      action: RunInstances
`), 0o600)
	require.NoError(t, err)

	profile, yaml, err := loadStartupTestProfile(path)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, 1, profile.Version)
	assert.Equal(t, "version: 1\nrules:\n  - when:\n      action: RunInstances", yaml)
}

func TestLoadStartupTestProfileRejectsDirectory(t *testing.T) {
	t.Parallel()

	_, _, err := loadStartupTestProfile(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
}

func TestLoadStartupTestProfileRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	_, _, err := loadStartupTestProfile("this-is-not-a-profile")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test profile")
}
