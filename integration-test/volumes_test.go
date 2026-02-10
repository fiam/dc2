package dc2_test

import (
	"context"
	"os"
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
			// Keep this pinned: test container must stay running and include
			// mkfs.ext4 so we can format the attached block device.
			imageID = "redis:7.4.2-bookworm"
		)

		runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String(imageID),
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
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
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
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.DeleteVolume(cleanupCtx, &ec2.DeleteVolumeInput{VolumeId: volume.VolumeId})
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
		cmd := dockerCommandContext(ctx, e.DockerHost, "exec", containerID, "mkfs.ext4", deviceName)
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

func TestDescribeVolumes(t *testing.T) {
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

		assertDescribeVolumes := func(t *testing.T, resp *ec2.DescribeVolumesOutput, err error) {
			require.NoError(t, err)
			require.NotEmpty(t, resp.Volumes)
			require.Len(t, resp.Volumes, 1)

			assert.Equal(t, *volume.VolumeId, *resp.Volumes[0].VolumeId)
			assert.Equal(t, *volume.Size, *resp.Volumes[0].Size)
			assert.Equal(t, *volume.Throughput, *resp.Volumes[0].Throughput)
		}

		resp1, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			VolumeIds: []string{*volume.VolumeId},
		})
		assertDescribeVolumes(t, resp1, err)

		resp2, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			MaxResults: aws.Int32(1),
		})
		assertDescribeVolumes(t, resp2, err)

		resp3, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			MaxResults: aws.Int32(0),
		})
		require.NoError(t, err)
		require.Empty(t, resp3.Volumes)
		require.NotEmpty(t, resp3.NextToken)

		_, err = e.Client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
			VolumeId: volume.VolumeId,
		})
		require.NoError(t, err)
	})
}

func TestDescribeVolumesPagination(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		const (
			az               = "us-east-1a"
			volumeSize       = 10
			volumeThroughput = 100
			volumeCount      = 5
		)

		var volumeIDs []string

		for range volumeCount {
			volume, err := e.Client.CreateVolume(ctx, &ec2.CreateVolumeInput{
				AvailabilityZone: aws.String(az),
				Size:             aws.Int32(volumeSize),
				Throughput:       aws.Int32(volumeThroughput),
				TagSpecifications: []ec2types.TagSpecification{
					{
						ResourceType: ec2types.ResourceTypeVolume,
					},
				},
			})
			require.NoError(t, err)
			require.NotNil(t, volume)
			require.NotNil(t, volume.VolumeId)
			volumeIDs = append(volumeIDs, *volume.VolumeId)
		}

		var nextToken *string
		var allVolumes []ec2types.Volume
		for {
			resp, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
				MaxResults: aws.Int32(1),
				NextToken:  nextToken,
			})
			require.NoError(t, err)
			allVolumes = append(allVolumes, resp.Volumes...)
			nextToken = resp.NextToken
			if nextToken == nil {
				break
			}
		}

		require.Len(t, allVolumes, volumeCount)
		for _, volume := range allVolumes {
			assert.Contains(t, volumeIDs, *volume.VolumeId)
			assert.Equal(t, int32(volumeSize), *volume.Size)
			assert.Equal(t, int32(volumeThroughput), *volume.Throughput)
		}
		for _, volumeID := range volumeIDs {
			_, err := e.Client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{
				VolumeId: aws.String(volumeID),
			})
			require.NoError(t, err)
		}
	})
}
