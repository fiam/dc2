package dc2_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateDeleteVolume(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		const (
			az       = "us-west-2a"
			tagName  = "Name"
			tagValue = "test-volume"
		)
		volume, err := e.Client.CreateVolume(ctx, &ec2.CreateVolumeInput{
			AvailabilityZone: aws.String(az),
			Size:             aws.Int32(1),
			Throughput:       aws.Int32(250),
			TagSpecifications: []ec2types.TagSpecification{
				{
					ResourceType: ec2types.ResourceTypeVolume,
					Tags: []ec2types.Tag{
						{
							Key:   aws.String(tagName),
							Value: aws.String(tagValue),
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, volume)
		require.NotNil(t, volume.VolumeId)

		_, err = e.Client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
			VolumeId: volume.VolumeId,
		})
		require.NoError(t, err)
	})
}

func TestAttachVolume(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		const (
			tagName    = "Name"
			tagValue   = "test-volume"
			deviceName = "/dev/sdf"
			volumeSize = 1
		)

		runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: ec2types.InstanceTypeA1Large,
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)

		require.NotEmpty(t, runInstancesOutput.Instances)
		instance := runInstancesOutput.Instances[0]
		require.NotNil(t, instance.InstanceId)
		instanceID := *instance.InstanceId
		require.NotNil(t, instance.Placement)
		require.NotNil(t, instance.Placement.AvailabilityZone)
		availabilityZone := *instance.Placement.AvailabilityZone

		t.Cleanup(func() {
			_, err := e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			assert.NoError(t, err)
		})

		volume, err := e.Client.CreateVolume(ctx, &ec2.CreateVolumeInput{
			AvailabilityZone: aws.String(availabilityZone),
			Size:             aws.Int32(volumeSize),
			Throughput:       aws.Int32(250),
			TagSpecifications: []ec2types.TagSpecification{
				{
					ResourceType: ec2types.ResourceTypeVolume,
					Tags: []ec2types.Tag{
						{
							Key:   aws.String(tagName),
							Value: aws.String(tagValue),
						},
					},
				},
			},
		})

		require.NoError(t, err)
		require.NotNil(t, volume.VolumeId)
		require.NotEmpty(t, *volume.VolumeId)
		require.NotNil(t, volume.Size)
		assert.Equal(t, int32(volumeSize), *volume.Size)

		t.Cleanup(func() {
			_, err := e.Client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: volume.VolumeId})
			assert.NoError(t, err)
		})

		describeVolumeResponse, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []string{*volume.VolumeId},
		})
		require.NoError(t, err)
		require.NotEmpty(t, describeVolumeResponse.Volumes)
		require.Len(t, describeVolumeResponse.Volumes, 1)
		require.NotNil(t, describeVolumeResponse.Volumes[0].Size)
		assert.Equal(t, int32(volumeSize), *describeVolumeResponse.Volumes[0].Size)
		assert.Empty(t, describeVolumeResponse.Volumes[0].Attachments)

		attachResponse, err := e.Client.AttachVolume(ctx, &ec2.AttachVolumeInput{
			Device:     aws.String(deviceName),
			InstanceId: aws.String(instanceID),
			VolumeId:   volume.VolumeId,
		})

		require.NoError(t, err)

		require.NotNil(t, attachResponse.InstanceId)
		assert.Equal(t, instanceID, *attachResponse.InstanceId)
		require.NotNil(t, attachResponse.VolumeId)
		assert.Equal(t, *volume.VolumeId, *attachResponse.VolumeId)
		require.NotNil(t, attachResponse.AttachTime)
		assert.WithinDuration(t, *attachResponse.AttachTime, time.Now(), 5*time.Second)
		require.NotNil(t, attachResponse.Device)
		assert.Equal(t, deviceName, *attachResponse.Device)
		require.NotNil(t, attachResponse.AttachTime)
		assert.NotZero(t, *attachResponse.AttachTime)

		// Now the volume must have an attachment
		describeVolumeResponse2, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []string{*volume.VolumeId},
		})
		require.NoError(t, err)
		require.Len(t, describeVolumeResponse2.Volumes, 1)
		require.Len(t, describeVolumeResponse2.Volumes[0].Attachments, 1)
		require.NotNil(t, describeVolumeResponse2.Volumes[0].Attachments[0].AttachTime)
		assert.Equal(t, *attachResponse.AttachTime, *describeVolumeResponse2.Volumes[0].Attachments[0].AttachTime)
		require.NotNil(t, describeVolumeResponse2.Volumes[0].Attachments[0].Device)
		assert.Equal(t, *attachResponse.Device, *describeVolumeResponse2.Volumes[0].Attachments[0].Device)
		require.NotNil(t, describeVolumeResponse2.Volumes[0].Attachments[0].InstanceId)
		assert.Equal(t, *attachResponse.InstanceId, *describeVolumeResponse2.Volumes[0].Attachments[0].InstanceId)

		// mkfs the device and mount the volume
		containerID := (*instance.InstanceId)[2:]
		cmd := exec.Command("docker", "exec", containerID, "mkfs.ext4", deviceName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Run())

		detachResponse, err := e.Client.DetachVolume(ctx, &ec2.DetachVolumeInput{
			VolumeId:   volume.VolumeId,
			Device:     aws.String(deviceName),
			InstanceId: aws.String(instanceID),
		})
		require.NoError(t, err)
		require.NotNil(t, detachResponse.Device)
		assert.Equal(t, deviceName, *detachResponse.Device)
		require.NotNil(t, detachResponse.InstanceId)
		assert.Equal(t, instanceID, *detachResponse.InstanceId)
		require.NotNil(t, detachResponse.VolumeId)
		assert.Equal(t, *volume.VolumeId, *detachResponse.VolumeId)
		require.NotNil(t, detachResponse.AttachTime)
		assert.Equal(t, *attachResponse.AttachTime, *detachResponse.AttachTime)
	})
}
