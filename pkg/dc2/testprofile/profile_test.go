package testprofile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelayWildcardMarketType(t *testing.T) {
	t.Parallel()

	profile := &Profile{
		Version: Version1,
		Rules: []Rule{{
			When: RuleWhen{Action: "RunInstances"},
			Delay: DelaySpec{
				Before: DelayHooks{Start: &Duration{Duration: 250 * time.Millisecond}},
			},
		}},
	}

	onDemand := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: "RunInstances", MarketType: "on-demand"})
	spot := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: "RunInstances", MarketType: "spot"})

	assert.Equal(t, 250*time.Millisecond, onDemand)
	assert.Equal(t, 250*time.Millisecond, spot)
}

func TestDelayMarketFilter(t *testing.T) {
	t.Parallel()

	marketType := "spot"
	profile := &Profile{
		Version: Version1,
		Rules: []Rule{{
			When: RuleWhen{
				Action:  "RunInstances",
				Request: &RequestFilters{Market: &MarketFilters{Type: &marketType}},
			},
			Delay: DelaySpec{
				Before: DelayHooks{Start: &Duration{Duration: time.Second}},
			},
		}},
	}

	onDemand := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: "RunInstances", MarketType: "on-demand"})
	spot := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: "RunInstances", MarketType: "spot"})

	assert.Zero(t, onDemand)
	assert.Equal(t, time.Second, spot)
}

func TestDelayInstanceFilters(t *testing.T) {
	t.Parallel()

	glob := "m7g.*"
	vcpuMin := 2
	vcpuMax := 4
	memoryMin := 4096
	profile := &Profile{
		Version: Version1,
		Rules: []Rule{{
			When: RuleWhen{
				Action: "RunInstances",
				Instance: &InstanceFilters{
					Type:      &StringMatcher{Glob: &glob},
					VCPU:      &IntRange{GTE: &vcpuMin, LTE: &vcpuMax},
					MemoryMiB: &IntRange{GTE: &memoryMin},
				},
			},
			Delay: DelaySpec{
				Before: DelayHooks{Allocate: &Duration{Duration: 2 * time.Second}},
			},
		}},
	}

	matched := profile.Delay(HookBefore, PhaseAllocate, MatchInput{
		Action:       "RunInstances",
		InstanceType: "m7g.large",
		VCPU:         2,
		MemoryMiB:    8192,
	})
	notMatched := profile.Delay(HookBefore, PhaseAllocate, MatchInput{
		Action:       "RunInstances",
		InstanceType: "t3.micro",
		VCPU:         2,
		MemoryMiB:    1024,
	})

	assert.Equal(t, 2*time.Second, matched)
	assert.Zero(t, notMatched)
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "profile.yaml")
	err := os.WriteFile(profilePath, []byte(`
version: 1
rules:
  - name: sample
    when:
      action: RunInstances
      request:
        market:
          type: spot
      instance:
        type:
          glob: m7g.*
        vcpu:
          gte: 2
        memory_mib:
          gte: 4096
    delay:
      before:
        allocate: 100ms
        start: 200ms
      after:
        start: 50ms
    reclaim:
      after: 10m
      notice: 30s
`), 0o600)
	require.NoError(t, err)

	profile, err := LoadFile(profilePath)
	require.NoError(t, err)
	require.NotNil(t, profile)

	d := profile.Delay(HookBefore, PhaseStart, MatchInput{
		Action:       "RunInstances",
		MarketType:   "spot",
		InstanceType: "m7g.large",
		VCPU:         2,
		MemoryMiB:    4096,
	})
	assert.Equal(t, 200*time.Millisecond, d)

	spot := profile.SpotReclaim(MatchInput{
		Action:       "RunInstances",
		MarketType:   "spot",
		InstanceType: "m7g.large",
		VCPU:         2,
		MemoryMiB:    4096,
	})
	require.NotNil(t, spot.After)
	require.NotNil(t, spot.Notice)
	assert.Equal(t, 10*time.Minute, *spot.After)
	assert.Equal(t, 30*time.Second, *spot.Notice)
}

func TestSpotReclaimByRules(t *testing.T) {
	t.Parallel()

	spot := "spot"
	instanceType := "c6i.large"
	profile := &Profile{
		Version: Version1,
		Rules: []Rule{
			{
				When: RuleWhen{
					Action:  "RunInstances",
					Request: &RequestFilters{Market: &MarketFilters{Type: &spot}},
				},
				SpotReclaim: SpotReclaimSpec{
					After:  &Duration{Duration: 2 * time.Minute},
					Notice: &Duration{Duration: 45 * time.Second},
				},
			},
			{
				When: RuleWhen{
					Action: "RunInstances",
					Request: &RequestFilters{
						Market: &MarketFilters{Type: &spot},
					},
					Instance: &InstanceFilters{
						Type: &StringMatcher{Equals: &instanceType},
					},
				},
				SpotReclaim: SpotReclaimSpec{
					After: &Duration{Duration: 90 * time.Second},
				},
			},
		},
	}

	base := profile.SpotReclaim(MatchInput{
		Action:       "RunInstances",
		MarketType:   "spot",
		InstanceType: "m7i.large",
	})
	require.NotNil(t, base.After)
	require.NotNil(t, base.Notice)
	assert.Equal(t, 2*time.Minute, *base.After)
	assert.Equal(t, 45*time.Second, *base.Notice)

	overridden := profile.SpotReclaim(MatchInput{
		Action:       "RunInstances",
		MarketType:   "spot",
		InstanceType: "c6i.large",
	})
	require.NotNil(t, overridden.After)
	require.NotNil(t, overridden.Notice)
	assert.Equal(t, 90*time.Second, *overridden.After)
	assert.Equal(t, 45*time.Second, *overridden.Notice)

	onDemand := profile.SpotReclaim(MatchInput{
		Action:       "RunInstances",
		MarketType:   "on-demand",
		InstanceType: "c6i.large",
	})
	assert.Nil(t, onDemand.After)
	assert.Nil(t, onDemand.Notice)
}

func TestLoadFileRejectsNegativeSpotReclaimDurations(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "profile.yaml")
	err := os.WriteFile(profilePath, []byte(`
version: 1
rules:
  - when:
      action: RunInstances
    reclaim:
      after: -1s
`), 0o600)
	require.NoError(t, err)

	_, err = LoadFile(profilePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reclaim.after must be >= 0")
}

func TestExampleProfilesLoad(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	examplesDir := filepath.Join(repoRoot, "examples", "test-profiles")

	entries, err := os.ReadDir(examplesDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(examplesDir, entry.Name())
			profile, loadErr := LoadFile(path)
			require.NoError(t, loadErr)
			require.NotNil(t, profile)
		})
	}
}
