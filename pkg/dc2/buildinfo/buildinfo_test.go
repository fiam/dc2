package buildinfo

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectDefaultsWhenBuildInfoUnavailable(t *testing.T) {
	t.Parallel()

	prevVersion := Version
	prevReadBuild := readBuild
	t.Cleanup(func() {
		Version = prevVersion
		readBuild = prevReadBuild
	})

	Version = ""
	readBuild = func() (*debug.BuildInfo, bool) {
		return nil, false
	}

	info := detect()
	assert.Equal(t, defaultVersion, info.Version)
	assert.Empty(t, info.Commit)
	assert.Empty(t, info.CommitTime)
	assert.False(t, info.Dirty)
	assert.Empty(t, info.GoVersion)
}

func TestApplyBuildInfoPopulatesVCSDetails(t *testing.T) {
	t.Parallel()

	info := applyBuildInfo(Info{Version: defaultVersion}, &debug.BuildInfo{
		GoVersion: "go1.26.0",
		Main: debug.Module{
			Version: "(devel)",
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-02-15T00:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	})

	assert.Equal(t, defaultVersion, info.Version)
	assert.Equal(t, "abc123", info.Commit)
	assert.Equal(t, "2026-02-15T00:00:00Z", info.CommitTime)
	assert.True(t, info.Dirty)
	assert.Equal(t, "go1.26.0", info.GoVersion)
}

func TestApplyBuildInfoUsesMainVersionWhenUnset(t *testing.T) {
	t.Parallel()

	info := applyBuildInfo(Info{Version: defaultVersion}, &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
	})
	assert.Equal(t, "v1.2.3", info.Version)

	info = applyBuildInfo(Info{Version: "v9.9.9"}, &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
	})
	assert.Equal(t, "v9.9.9", info.Version)
}
