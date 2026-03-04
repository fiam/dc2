package dc2

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/testprofile"
)

func testDispatcherWithRunInstancesStartDelay(delay time.Duration) *Dispatcher {
	return &Dispatcher{
		testProfileUpdateCh: make(chan struct{}, 1),
		testProfile: &testprofile.Profile{
			Version: testprofile.Version1,
			Rules: []testprofile.Rule{
				{
					Name: "delayed-start",
					When: testprofile.RuleWhen{
						Action: testprofile.ActionRunInstances,
						Instance: &testprofile.InstanceFilters{
							Type: &testprofile.StringMatcher{Equals: new("delay-type")},
						},
					},
					Delay: testprofile.DelaySpec{
						Before: testprofile.DelayHooks{
							Start: &testprofile.Duration{Duration: delay},
						},
					},
				},
			},
		},
	}
}

func delayMatchInput() testprofile.MatchInput {
	return testprofile.MatchInput{
		Action:       testprofile.ActionRunInstances,
		InstanceType: "delay-type",
	}
}

func TestApplyTestProfileDelayForMatchInputsUsesBubbleClock(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		dispatch := testDispatcherWithRunInstancesStartDelay(1500 * time.Millisecond)

		started := time.Now()
		err := dispatch.applyTestProfileDelayForMatchInputsInternal(
			t.Context(),
			testprofile.HookBefore,
			testprofile.PhaseStart,
			[]testprofile.MatchInput{delayMatchInput()},
			false,
		)
		require.NoError(t, err)
		assert.Equal(t, 1500*time.Millisecond, time.Since(started))
	})
}

func TestApplyTestProfileDelayForMatchInputsUnblocksOnProfileUpdate(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		dispatch := testDispatcherWithRunInstancesStartDelay(time.Hour)
		errCh := make(chan error, 1)

		go func() {
			errCh <- dispatch.applyTestProfileDelayForMatchInputsInternal(
				t.Context(),
				testprofile.HookBefore,
				testprofile.PhaseStart,
				[]testprofile.MatchInput{delayMatchInput()},
				false,
			)
		}()

		synctest.Wait()
		dispatch.clearTestProfile()
		synctest.Wait()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		default:
			t.Fatal("delay wait did not complete after clearing test profile")
		}
	})
}

func TestApplyTestProfileDelayForMatchInputsReturnsContextError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		dispatch := testDispatcherWithRunInstancesStartDelay(time.Hour)
		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)

		go func() {
			errCh <- dispatch.applyTestProfileDelayForMatchInputsInternal(
				ctx,
				testprofile.HookBefore,
				testprofile.PhaseStart,
				[]testprofile.MatchInput{delayMatchInput()},
				false,
			)
		}()

		synctest.Wait()
		cancel()
		synctest.Wait()

		select {
		case err := <-errCh:
			require.ErrorIs(t, err, context.Canceled)
		default:
			t.Fatal("delay wait did not complete after canceling context")
		}
	})
}
