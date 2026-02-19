package testprofile

import (
	"os"
	"path/filepath"
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
}
