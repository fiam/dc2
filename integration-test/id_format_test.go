package dc2_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceIDFormats(t *testing.T) {
	t.Parallel()

	instanceIDPattern := regexp.MustCompile(`^i-[0-9a-f]{17}$`)
	volumeIDPattern := regexp.MustCompile(`^vol-[0-9a-f]{17}$`)
	launchTemplateIDPattern := regexp.MustCompile(`^lt-[0-9a-f]{17}$`)

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "my-type",
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runResp.Instances, 1)
		require.NotNil(t, runResp.Instances[0].InstanceId)
		instanceID := *runResp.Instances[0].InstanceId
		assert.Regexp(t, instanceIDPattern, instanceID)
		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, terminateErr := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, terminateErr)
		})

		volumeResp, err := e.Client.CreateVolume(ctx, &ec2.CreateVolumeInput{
			AvailabilityZone: aws.String("us-east-1a"),
			Size:             aws.Int32(1),
		})
		require.NoError(t, err)
		require.NotNil(t, volumeResp.VolumeId)
		volumeID := *volumeResp.VolumeId
		assert.Regexp(t, volumeIDPattern, volumeID)
		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, deleteErr := e.Client.DeleteVolume(cleanupCtx, &ec2.DeleteVolumeInput{
				VolumeId: aws.String(volumeID),
			})
			require.NoError(t, deleteErr)
		})

		launchTemplateName := fmt.Sprintf("lt-id-format-%d", time.Now().UnixNano())
		launchTemplateResp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceType("my-type"),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, launchTemplateResp.LaunchTemplate)
		require.NotNil(t, launchTemplateResp.LaunchTemplate.LaunchTemplateId)
		launchTemplateID := *launchTemplateResp.LaunchTemplate.LaunchTemplateId
		assert.Regexp(t, launchTemplateIDPattern, launchTemplateID)
		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, deleteErr := e.Client.DeleteLaunchTemplate(cleanupCtx, &ec2.DeleteLaunchTemplateInput{
				LaunchTemplateId: aws.String(launchTemplateID),
			})
			require.NoError(t, deleteErr)
		})
	})
}
