package dc2_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2"
)

func TestSpotInstanceLifecycleAndSimulatedReclaim(t *testing.T) {
	t.Parallel()

	mode := configuredTestMode()
	reclaimAfter := 1200 * time.Millisecond
	reclaimNotice := 800 * time.Millisecond
	serverOpts, serverEnv := spotReclaimConfig(mode, reclaimAfter, reclaimNotice)

	testWithServerWithOptionsAndEnvForMode(t, mode, serverOpts, serverEnv, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: ec2types.InstanceType("my-type"),
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

		t.Cleanup(func() {
			apiCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, _ = e.Client.TerminateInstances(apiCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
		})

		require.NotNil(t, runResp.Instances[0].InstanceLifecycle)
		assert.Equal(
			t,
			string(ec2types.InstanceLifecycleTypeSpot),
			string(runResp.Instances[0].InstanceLifecycle),
		)

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
			if *instance.StateReason.Code != "Server.SpotInstanceTermination" {
				return false
			}
			return instance.StateTransitionReason != nil &&
				strings.Contains(*instance.StateTransitionReason, "Server.SpotInstanceTermination")
		}, 8*time.Second, 100*time.Millisecond)
	})
}

func TestSpotInstanceIMDSInterruptionAction(t *testing.T) {
	t.Parallel()
	requireContainerModeForIMDSTest(t)

	reclaimAfter := 2500 * time.Millisecond
	reclaimNotice := 2 * time.Second
	serverOpts, serverEnv := spotReclaimConfig(configuredTestMode(), reclaimAfter, reclaimNotice)

	testWithServerWithOptionsAndEnvForMode(t, configuredTestMode(), serverOpts, serverEnv, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: ec2types.InstanceType("my-type"),
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
		containerID := containerIDForInstanceID(t, ctx, e.DockerHost, instanceID)
		token := fetchIMDSToken(t, ctx, e.DockerHost, containerID)

		assert.Eventually(t, func() bool {
			out, curlErr := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/spot/instance-action", token)
			if curlErr != nil {
				return false
			}
			payload := map[string]string{}
			if err := json.Unmarshal(out, &payload); err != nil {
				return false
			}
			if payload["action"] != "terminate" {
				return false
			}
			_, err := time.Parse(time.RFC3339, payload["time"])
			return err == nil
		}, 6*time.Second, 100*time.Millisecond)

		assert.Eventually(t, func() bool {
			out, curlErr := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/spot/termination-time", token)
			if curlErr != nil {
				return false
			}
			_, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
			return err == nil
		}, 6*time.Second, 100*time.Millisecond)

		assert.Eventually(t, func() bool {
			out, describeErr := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			if describeErr != nil || len(out.Reservations) == 0 || len(out.Reservations[0].Instances) == 0 {
				return false
			}
			instance := out.Reservations[0].Instances[0]
			return instance.State != nil && instance.State.Name == ec2types.InstanceStateNameTerminated
		}, 8*time.Second, 100*time.Millisecond)
	})
}

func spotReclaimConfig(mode testMode, reclaimAfter time.Duration, reclaimNotice time.Duration) ([]dc2.Option, map[string]string) {
	if mode == testModeHost {
		return []dc2.Option{
			dc2.WithSpotReclaimAfter(reclaimAfter),
			dc2.WithSpotReclaimNotice(reclaimNotice),
		}, nil
	}
	return nil, map[string]string{
		"DC2_SPOT_RECLAIM_AFTER":  reclaimAfter.String(),
		"DC2_SPOT_RECLAIM_NOTICE": reclaimNotice.String(),
	}
}
