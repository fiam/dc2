package testprofile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDelayWildcardMarketType(t *testing.T) {
	t.Parallel()

	profile := &Profile{
		Version: Version1,
		Rules: []Rule{{
			When: RuleWhen{Action: ActionRunInstances},
			Delay: DelaySpec{
				Before: DelayHooks{Start: &Duration{Duration: 250 * time.Millisecond}},
			},
		}},
	}

	onDemand := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: ActionRunInstances, MarketType: "on-demand"})
	spot := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: ActionRunInstances, MarketType: "spot"})

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
				Action:  ActionRunInstances,
				Request: &RequestFilters{Market: &MarketFilters{Type: &marketType}},
			},
			Delay: DelaySpec{
				Before: DelayHooks{Start: &Duration{Duration: time.Second}},
			},
		}},
	}

	onDemand := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: ActionRunInstances, MarketType: "on-demand"})
	spot := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: ActionRunInstances, MarketType: "spot"})

	assert.Zero(t, onDemand)
	assert.Equal(t, time.Second, spot)
}

func TestDelayAutoScalingGroupFilter(t *testing.T) {
	t.Parallel()

	name := "asg-freeze"
	profile := &Profile{
		Version: Version1,
		Rules: []Rule{{
			When: RuleWhen{
				Action: ActionRunInstances,
				Request: &RequestFilters{
					AutoScaling: &AutoScalingFilters{
						Group: &AutoScalingGroupFilters{
							Name: &StringMatcher{Equals: &name},
						},
					},
				},
			},
			Delay: DelaySpec{
				Before: DelayHooks{Allocate: &Duration{Duration: 2 * time.Second}},
			},
		}},
	}

	matching := profile.Delay(HookBefore, PhaseAllocate, MatchInput{
		Action:               ActionRunInstances,
		AutoScalingGroupName: "asg-freeze",
	})
	nonMatching := profile.Delay(HookBefore, PhaseAllocate, MatchInput{
		Action:               ActionRunInstances,
		AutoScalingGroupName: "asg-other",
	})
	directRunInstances := profile.Delay(HookBefore, PhaseAllocate, MatchInput{
		Action: ActionRunInstances,
	})

	assert.Equal(t, 2*time.Second, matching)
	assert.Zero(t, nonMatching)
	assert.Zero(t, directRunInstances)
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
				Action: ActionRunInstances,
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
		Action:       ActionRunInstances,
		InstanceType: "m7g.large",
		VCPU:         2,
		MemoryMiB:    8192,
	})
	notMatched := profile.Delay(HookBefore, PhaseAllocate, MatchInput{
		Action:       ActionRunInstances,
		InstanceType: "t3.micro",
		VCPU:         2,
		MemoryMiB:    1024,
	})

	assert.Equal(t, 2*time.Second, matched)
	assert.Zero(t, notMatched)
}

func TestDelayLifecyclePhases(t *testing.T) {
	t.Parallel()

	profile := &Profile{
		Version: Version1,
		Rules: []Rule{
			{
				When: RuleWhen{Action: ActionStartInstances},
				Delay: DelaySpec{
					Before: DelayHooks{Start: &Duration{Duration: 120 * time.Millisecond}},
					After:  DelayHooks{Start: &Duration{Duration: 80 * time.Millisecond}},
				},
			},
			{
				When: RuleWhen{Action: ActionStopInstances},
				Delay: DelaySpec{
					Before: DelayHooks{Stop: &Duration{Duration: 300 * time.Millisecond}},
				},
			},
			{
				When: RuleWhen{Action: ActionTerminateInstances},
				Delay: DelaySpec{
					After: DelayHooks{Terminate: &Duration{Duration: 450 * time.Millisecond}},
				},
			},
		},
	}

	startBefore := profile.Delay(HookBefore, PhaseStart, MatchInput{Action: ActionStartInstances})
	startAfter := profile.Delay(HookAfter, PhaseStart, MatchInput{Action: ActionStartInstances})
	stopBefore := profile.Delay(HookBefore, PhaseStop, MatchInput{Action: ActionStopInstances})
	terminateAfter := profile.Delay(HookAfter, PhaseTerminate, MatchInput{Action: ActionTerminateInstances})

	assert.Equal(t, 120*time.Millisecond, startBefore)
	assert.Equal(t, 80*time.Millisecond, startAfter)
	assert.Equal(t, 300*time.Millisecond, stopBefore)
	assert.Equal(t, 450*time.Millisecond, terminateAfter)

	assert.Zero(t, profile.Delay(HookBefore, PhaseStop, MatchInput{Action: ActionStartInstances}))
	assert.Zero(t, profile.Delay(HookAfter, PhaseTerminate, MatchInput{Action: ActionStopInstances}))
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
        stop: 300ms
      after:
        start: 50ms
        terminate: 150ms
    reclaim:
      after: 10m
      notice: 30s
`), 0o600)
	require.NoError(t, err)

	profile, err := LoadFile(profilePath)
	require.NoError(t, err)
	require.NotNil(t, profile)

	d := profile.Delay(HookBefore, PhaseStart, MatchInput{
		Action:       ActionRunInstances,
		MarketType:   "spot",
		InstanceType: "m7g.large",
		VCPU:         2,
		MemoryMiB:    4096,
	})
	assert.Equal(t, 200*time.Millisecond, d)
	assert.Equal(t, 300*time.Millisecond, profile.Delay(HookBefore, PhaseStop, MatchInput{
		Action:       ActionRunInstances,
		MarketType:   "spot",
		InstanceType: "m7g.large",
		VCPU:         2,
		MemoryMiB:    4096,
	}))
	assert.Equal(t, 150*time.Millisecond, profile.Delay(HookAfter, PhaseTerminate, MatchInput{
		Action:       ActionRunInstances,
		MarketType:   "spot",
		InstanceType: "m7g.large",
		VCPU:         2,
		MemoryMiB:    4096,
	}))

	spot := profile.SpotReclaim(MatchInput{
		Action:       ActionRunInstances,
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

func TestDurationMarshalYAML(t *testing.T) {
	t.Parallel()

	profile := Profile{
		Version: Version1,
		Rules: []Rule{
			{
				When: RuleWhen{Action: ActionRunInstances},
				Delay: DelaySpec{
					Before: DelayHooks{
						Allocate: &Duration{Duration: time.Hour},
						Start:    &Duration{Duration: 0},
					},
				},
			},
		},
	}

	raw, err := yaml.Marshal(profile)
	require.NoError(t, err)
	serialized := string(raw)
	assert.Contains(t, serialized, "allocate: 1h0m0s")
	assert.Contains(t, serialized, "start: 0s")
	assert.NotContains(t, serialized, "Duration:")
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
					Action:  ActionRunInstances,
					Request: &RequestFilters{Market: &MarketFilters{Type: &spot}},
				},
				SpotReclaim: SpotReclaimSpec{
					After:  &Duration{Duration: 2 * time.Minute},
					Notice: &Duration{Duration: 45 * time.Second},
				},
			},
			{
				When: RuleWhen{
					Action: ActionRunInstances,
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
		Action:       ActionRunInstances,
		MarketType:   "spot",
		InstanceType: "m7i.large",
	})
	require.NotNil(t, base.After)
	require.NotNil(t, base.Notice)
	assert.Equal(t, 2*time.Minute, *base.After)
	assert.Equal(t, 45*time.Second, *base.Notice)

	overridden := profile.SpotReclaim(MatchInput{
		Action:       ActionRunInstances,
		MarketType:   "spot",
		InstanceType: "c6i.large",
	})
	require.NotNil(t, overridden.After)
	require.NotNil(t, overridden.Notice)
	assert.Equal(t, 90*time.Second, *overridden.After)
	assert.Equal(t, 45*time.Second, *overridden.Notice)

	onDemand := profile.SpotReclaim(MatchInput{
		Action:       ActionRunInstances,
		MarketType:   "on-demand",
		InstanceType: "c6i.large",
	})
	assert.Nil(t, onDemand.After)
	assert.Nil(t, onDemand.Notice)
}

func TestLoadYAML(t *testing.T) {
	t.Parallel()

	profile, err := LoadYAML(`
version: 1
rules:
  - when:
      action: RunInstances
    delay:
      before:
        start: 150ms
`)
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Equal(t, Version1, profile.Version)
	assert.Len(t, profile.Rules, 1)
}

func TestLoadYAMLEmpty(t *testing.T) {
	t.Parallel()

	_, err := LoadYAML(" \n ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test profile YAML is empty")
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

func TestLoadFileRejectsInvalidAutoScalingGroupMatcher(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "profile.yaml")
	err := os.WriteFile(profilePath, []byte(`
version: 1
rules:
  - when:
      action: RunInstances
      request:
        autoscaling:
          group:
            name:
              equals: asg-a
              glob: asg-*
`), 0o600)
	require.NoError(t, err)

	_, err = LoadFile(profilePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request.autoscaling.group.name cannot define both equals and glob")
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
