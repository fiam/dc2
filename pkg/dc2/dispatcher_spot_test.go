package dc2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/testprofile"
)

func TestNormalizeMarketType(t *testing.T) {
	t.Parallel()

	t.Run("defaults to on-demand", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeMarketType("")
		require.NoError(t, err)
		assert.Equal(t, instanceMarketTypeOnDemand, got)
	})

	t.Run("accepts spot", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeMarketType("spot")
		require.NoError(t, err)
		assert.Equal(t, instanceMarketTypeSpot, got)
	})

	t.Run("accepts on-demand alias", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeMarketType("ON-DEMAND")
		require.NoError(t, err)
		assert.Equal(t, instanceMarketTypeOnDemand, got)
	})

	t.Run("rejects unknown market type", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeMarketType("reserved")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "InstanceMarketOptions.MarketType")
	})
}

func TestResolveSpotReclaimPlan(t *testing.T) {
	t.Parallel()

	req := &api.RunInstancesRequest{
		InstanceType: "c6i.large",
	}

	t.Run("uses dispatcher defaults when no profile", func(t *testing.T) {
		t.Parallel()
		d := &Dispatcher{
			opts: DispatcherOptions{
				SpotReclaimAfter:  3 * time.Minute,
				SpotReclaimNotice: 30 * time.Second,
			},
		}

		plan := d.resolveSpotReclaimPlan(req, instanceMarketTypeSpot)
		assert.Equal(t, 3*time.Minute, plan.After)
		assert.Equal(t, 30*time.Second, plan.Notice)
	})

	t.Run("applies profile overrides", func(t *testing.T) {
		t.Parallel()
		spot := "spot"
		d := &Dispatcher{
			opts: DispatcherOptions{
				SpotReclaimAfter:  3 * time.Minute,
				SpotReclaimNotice: 30 * time.Second,
			},
			testProfile: &testprofile.Profile{
				Version: testprofile.Version1,
				Rules: []testprofile.Rule{{
					When: testprofile.RuleWhen{
						Action:  "RunInstances",
						Request: &testprofile.RequestFilters{Market: &testprofile.MarketFilters{Type: &spot}},
					},
					SpotReclaim: testprofile.SpotReclaimSpec{
						After:  &testprofile.Duration{Duration: 10 * time.Second},
						Notice: &testprofile.Duration{Duration: 2 * time.Second},
					},
				}},
			},
		}
		reqWithMarket := &api.RunInstancesRequest{
			InstanceType: "c6i.large",
			InstanceMarketOptions: &api.RunInstancesInstanceMarketOptions{
				MarketType: "spot",
			},
		}

		plan := d.resolveSpotReclaimPlan(reqWithMarket, instanceMarketTypeSpot)
		assert.Equal(t, 10*time.Second, plan.After)
		assert.Equal(t, 2*time.Second, plan.Notice)
	})

	t.Run("returns empty plan for non-spot markets", func(t *testing.T) {
		t.Parallel()
		d := &Dispatcher{
			opts: DispatcherOptions{
				SpotReclaimAfter:  3 * time.Minute,
				SpotReclaimNotice: 30 * time.Second,
			},
		}

		plan := d.resolveSpotReclaimPlan(req, instanceMarketTypeOnDemand)
		assert.Zero(t, plan.After)
		assert.Zero(t, plan.Notice)
	})
}
