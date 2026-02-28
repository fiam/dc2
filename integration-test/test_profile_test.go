package dc2_test

import (
	"context"
	"fmt"
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
