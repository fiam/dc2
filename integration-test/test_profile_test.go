package dc2_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2"
)

func TestRunInstancesAppliesProfileDelays(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-profile.yaml")
	err := os.WriteFile(profilePath, []byte(`
version: 1
rules:
  - name: delayed-start
    when:
      action: RunInstances
      instance:
        type:
          equals: delay-type
    delay:
      before:
        start: 1s
`), 0o600)
	require.NoError(t, err)

	testWithServerWithOptionsAndEnvForMode(
		t,
		testModeHost,
		[]dc2.Option{dc2.WithTestProfilePath(profilePath)},
		nil,
		func(t *testing.T, ctx context.Context, e *TestEnvironment) {
			runInstance := func(instanceType string) (time.Duration, string) {
				t.Helper()
				start := time.Now()
				resp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
					ImageId:      aws.String("nginx"),
					InstanceType: ec2types.InstanceType(instanceType),
					MinCount:     aws.Int32(1),
					MaxCount:     aws.Int32(1),
				})
				require.NoError(t, err)
				require.Len(t, resp.Instances, 1)
				return time.Since(start), aws.ToString(resp.Instances[0].InstanceId)
			}

			terminate := func(instanceID string) {
				t.Helper()
				apiCtx, cancel := cleanupAPICtx(t)
				defer cancel()
				_, err := e.Client.TerminateInstances(apiCtx, &ec2.TerminateInstancesInput{
					InstanceIds: []string{instanceID},
				})
				require.NoError(t, err)
			}

			baselineDuration, baselineID := runInstance("baseline-type")
			terminate(baselineID)

			delayedDuration, delayedID := runInstance("delay-type")
			terminate(delayedID)

			assert.GreaterOrEqual(t, delayedDuration-baselineDuration, 700*time.Millisecond)
		},
	)
}

func TestRunInstancesAppliesInlineProfileDelays(t *testing.T) {
	t.Parallel()

	profileYAML := `
version: 1
rules:
  - name: delayed-start-inline
    when:
      action: RunInstances
      instance:
        type:
          equals: inline-delay-type
    delay:
      before:
        start: 1s
`

	testWithServerWithOptionsAndEnvForMode(
		t,
		testModeHost,
		[]dc2.Option{dc2.WithTestProfileInput(profileYAML)},
		nil,
		func(t *testing.T, ctx context.Context, e *TestEnvironment) {
			runInstance := func(instanceType string) (time.Duration, string) {
				t.Helper()
				start := time.Now()
				resp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
					ImageId:      aws.String("nginx"),
					InstanceType: ec2types.InstanceType(instanceType),
					MinCount:     aws.Int32(1),
					MaxCount:     aws.Int32(1),
				})
				require.NoError(t, err)
				require.Len(t, resp.Instances, 1)
				return time.Since(start), aws.ToString(resp.Instances[0].InstanceId)
			}

			terminate := func(instanceID string) {
				t.Helper()
				apiCtx, cancel := cleanupAPICtx(t)
				defer cancel()
				_, err := e.Client.TerminateInstances(apiCtx, &ec2.TerminateInstancesInput{
					InstanceIds: []string{instanceID},
				})
				require.NoError(t, err)
			}

			baselineDuration, baselineID := runInstance("baseline-type-inline")
			terminate(baselineID)

			delayedDuration, delayedID := runInstance("inline-delay-type")
			terminate(delayedID)

			assert.GreaterOrEqual(t, delayedDuration-baselineDuration, 700*time.Millisecond)
		},
	)
}

func TestRunInstancesAppliesProfileSpotReclaim(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-profile.yaml")
	err := os.WriteFile(profilePath, []byte(`
version: 1
rules:
  - name: spot-reclaim
    when:
      action: RunInstances
      request:
        market:
          type: spot
    reclaim:
      after: 1200ms
      notice: 800ms
`), 0o600)
	require.NoError(t, err)

	testWithServerWithOptionsAndEnvForMode(
		t,
		testModeHost,
		[]dc2.Option{dc2.WithTestProfilePath(profilePath)},
		nil,
		func(t *testing.T, ctx context.Context, e *TestEnvironment) {
			runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceType("spot-profile-type"),
				MinCount:     aws.Int32(1),
				MaxCount:     aws.Int32(1),
				InstanceMarketOptions: &ec2types.InstanceMarketOptionsRequest{
					MarketType: ec2types.MarketTypeSpot,
				},
			})
			require.NoError(t, err)
			require.Len(t, runResp.Instances, 1)
			instanceID := aws.ToString(runResp.Instances[0].InstanceId)
			require.NotEmpty(t, instanceID)

			assert.Eventually(t, func() bool {
				out, describeErr := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
					InstanceIds: []string{instanceID},
				})
				if describeErr != nil || len(out.Reservations) == 0 || len(out.Reservations[0].Instances) == 0 {
					return false
				}
				instance := out.Reservations[0].Instances[0]
				if instance.State == nil || instance.State.Name != ec2types.InstanceStateNameTerminated {
					return false
				}
				if instance.StateReason == nil || instance.StateReason.Code == nil {
					return false
				}
				return *instance.StateReason.Code == "Server.SpotInstanceTermination"
			}, 8*time.Second, 100*time.Millisecond)
		},
	)
}

func TestWarmPoolAppliesProfileStartDelayBeforeStop(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "test-profile.yaml")
	err := os.WriteFile(profilePath, []byte(`
version: 1
rules:
  - name: delayed-warm-stop
    when:
      action: RunInstances
      instance:
        type:
          equals: warm-delay-type
    delay:
      after:
        start: 3s
`), 0o600)
	require.NoError(t, err)

	testWithServerWithOptionsAndEnvForMode(
		t,
		testModeHost,
		[]dc2.Option{dc2.WithTestProfilePath(profilePath)},
		nil,
		func(t *testing.T, ctx context.Context, e *TestEnvironment) {
			launchTemplateName := fmt.Sprintf(
				"lt-warm-delayed-%s",
				strings.ReplaceAll(t.Name(), "/", "-"),
			)
			autoScalingGroupName := fmt.Sprintf(
				"asg-warm-delayed-%s",
				strings.ReplaceAll(t.Name(), "/", "-"),
			)

			lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
				LaunchTemplateName: aws.String(launchTemplateName),
				LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
					ImageId:      aws.String("nginx"),
					InstanceType: ec2types.InstanceType("warm-delay-type"),
				},
			})
			require.NoError(t, err)
			require.NotNil(t, lt.LaunchTemplate)
			require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

			_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
				AutoScalingGroupName: aws.String(autoScalingGroupName),
				MinSize:              aws.Int32(1),
				MaxSize:              aws.Int32(2),
				DesiredCapacity:      aws.Int32(1),
				LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
					LaunchTemplateId: lt.LaunchTemplate.LaunchTemplateId,
					Version:          aws.String("$Default"),
				},
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				cleanupAutoScalingGroup(t, e, autoScalingGroupName)
			})

			start := time.Now()
			_, err = e.AutoScalingClient.PutWarmPool(ctx, &autoscaling.PutWarmPoolInput{
				AutoScalingGroupName:     aws.String(autoScalingGroupName),
				MinSize:                  aws.Int32(1),
				MaxGroupPreparedCapacity: aws.Int32(2),
				PoolState:                autoscalingtypes.WarmPoolStateStopped,
			})
			require.NoError(t, err)
			duration := time.Since(start)

			require.Eventually(t, func() bool {
				out, describeErr := e.AutoScalingClient.DescribeWarmPool(ctx, &autoscaling.DescribeWarmPoolInput{
					AutoScalingGroupName: aws.String(autoScalingGroupName),
				})
				if describeErr != nil || len(out.Instances) != 1 {
					return false
				}
				return out.Instances[0].LifecycleState == autoscalingtypes.LifecycleStateWarmedStopped
			}, 20*time.Second, 250*time.Millisecond)

			assert.GreaterOrEqual(t, duration, 2500*time.Millisecond)
		},
	)
}

func TestRuntimeTestProfileEndpointUpdatesRunInstancesBehavior(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runInstance := func(instanceType string) (time.Duration, string) {
			t.Helper()
			start := time.Now()
			resp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceType(instanceType),
				MinCount:     aws.Int32(1),
				MaxCount:     aws.Int32(1),
			})
			require.NoError(t, err)
			require.Len(t, resp.Instances, 1)
			require.NotNil(t, resp.Instances[0].InstanceId)
			return time.Since(start), aws.ToString(resp.Instances[0].InstanceId)
		}

		terminate := func(instanceID string) {
			t.Helper()
			apiCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(apiCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
		}

		getProfile := func() (string, int) {
			t.Helper()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Endpoint+"/_dc2/test-profile", nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, readErr := io.ReadAll(resp.Body)
			require.NoError(t, readErr)
			return string(body), resp.StatusCode
		}

		putProfileYAML := func(yaml string) {
			t.Helper()
			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodPut,
				e.Endpoint+"/_dc2/test-profile",
				strings.NewReader(yaml),
			)
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/yaml")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, readErr := io.ReadAll(resp.Body)
			require.NoError(t, readErr)
			require.Equal(t, http.StatusNoContent, resp.StatusCode, "response body=%s", string(body))
		}

		deleteProfile := func() {
			t.Helper()
			req, err := http.NewRequestWithContext(ctx, http.MethodDelete, e.Endpoint+"/_dc2/test-profile", nil)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, readErr := io.ReadAll(resp.Body)
			require.NoError(t, readErr)
			require.Equal(t, http.StatusNoContent, resp.StatusCode, "response body=%s", string(body))
		}

		warmupDuration, warmupInstanceID := runInstance("runtime-profile-type")
		terminate(warmupInstanceID)
		t.Logf("warmup run completed in %s", warmupDuration)

		initialBody, initialStatus := getProfile()
		assert.Equal(t, http.StatusNotFound, initialStatus)
		assert.Contains(t, initialBody, "no active test profile")

		baselineDuration, baselineInstanceID := runInstance("runtime-profile-type")
		terminate(baselineInstanceID)

		putProfileYAML(`
version: 1
rules:
  - name: runtime-delay
    when:
      action: RunInstances
      instance:
        type:
          equals: runtime-profile-type
    delay:
      before:
        start: 1500ms
`)
		activeYAML, activeStatus := getProfile()
		require.Equal(t, http.StatusOK, activeStatus)
		assert.Contains(t, activeYAML, "name: runtime-delay")
		assert.Contains(t, activeYAML, "start: 1500ms")

		delayedDuration, delayedInstanceID := runInstance("runtime-profile-type")
		terminate(delayedInstanceID)

		assert.GreaterOrEqual(t, delayedDuration-baselineDuration, 1000*time.Millisecond)

		deleteProfile()
		afterDeleteBody, afterDeleteStatus := getProfile()
		assert.Equal(t, http.StatusNotFound, afterDeleteStatus)
		assert.Contains(t, afterDeleteBody, "no active test profile")

		restoredDuration, restoredInstanceID := runInstance("runtime-profile-type")
		terminate(restoredInstanceID)

		assert.GreaterOrEqual(t, delayedDuration-restoredDuration, 1000*time.Millisecond)
	})
}
