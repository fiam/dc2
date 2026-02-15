package dc2_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoScalingGroupLifecycle(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(3),
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

		assertGroup := func(expectedDesired, expectedCount int32) {
			t.Helper()
			described, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			require.NoError(t, err)
			require.Len(t, described.AutoScalingGroups, 1)

			group := described.AutoScalingGroups[0]
			require.NotNil(t, group.AutoScalingGroupName)
			assert.Equal(t, autoScalingGroupName, *group.AutoScalingGroupName)
			require.NotNil(t, group.DesiredCapacity)
			assert.Equal(t, expectedDesired, *group.DesiredCapacity)
			require.NotNil(t, group.MinSize)
			require.NotNil(t, group.MaxSize)
			assert.Equal(t, int32(1), *group.MinSize)
			assert.Equal(t, int32(3), *group.MaxSize)
			require.NotNil(t, group.LaunchTemplate)
			require.NotNil(t, group.LaunchTemplate.LaunchTemplateId)
			assert.Equal(t, *lt.LaunchTemplate.LaunchTemplateId, *group.LaunchTemplate.LaunchTemplateId)

			require.Len(t, group.Instances, int(expectedCount))
			for _, instance := range group.Instances {
				require.NotNil(t, instance.InstanceId)
				assert.True(t, strings.HasPrefix(*instance.InstanceId, "i-"))
				require.NotNil(t, instance.HealthStatus)
				assert.Equal(t, "Healthy", *instance.HealthStatus)
				assert.Equal(t, autoscalingtypes.LifecycleStateInService, instance.LifecycleState)
			}
		}

		assertGroup(1, 1)

		_, err = e.AutoScalingClient.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			DesiredCapacity:      aws.Int32(2),
		})
		require.NoError(t, err)
		assertGroup(2, 2)

		_, err = e.AutoScalingClient.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			DesiredCapacity:      aws.Int32(1),
		})
		require.NoError(t, err)
		assertGroup(1, 1)

		_, err = e.AutoScalingClient.UpdateAutoScalingGroup(ctx, &autoscaling.UpdateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			DesiredCapacity:      aws.Int32(3),
		})
		require.NoError(t, err)
		assertGroup(3, 3)

		_, err = e.AutoScalingClient.DeleteAutoScalingGroup(ctx, &autoscaling.DeleteAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			ForceDelete:          aws.Bool(true),
		})
		require.NoError(t, err)

		described, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []string{autoScalingGroupName},
		})
		require.NoError(t, err)
		require.Empty(t, described.AutoScalingGroups)
	})
}

func TestAutoScalingGroupDescribeFiltersByTag(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-filter-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupMatch := fmt.Sprintf("asg-filter-match-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupOther := fmt.Sprintf("asg-filter-other-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		for _, groupName := range []string{autoScalingGroupMatch, autoScalingGroupOther} {
			_, err := e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
				AutoScalingGroupName: aws.String(groupName),
				MinSize:              aws.Int32(1),
				MaxSize:              aws.Int32(2),
				DesiredCapacity:      aws.Int32(1),
				LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
					LaunchTemplateId: lt.LaunchTemplate.LaunchTemplateId,
					Version:          aws.String("$Default"),
				},
			})
			require.NoError(t, err)
		}

		t.Cleanup(func() {
			cleanupAutoScalingGroup(t, e, autoScalingGroupMatch)
			cleanupAutoScalingGroup(t, e, autoScalingGroupOther)
		})

		_, err = e.AutoScalingClient.CreateOrUpdateTags(ctx, &autoscaling.CreateOrUpdateTagsInput{
			Tags: []autoscalingtypes.Tag{
				{
					Key:          aws.String("tcc.zone"),
					Value:        aws.String("e2e-aws-zone"),
					ResourceId:   aws.String(autoScalingGroupMatch),
					ResourceType: aws.String("auto-scaling-group"),
				},
				{
					Key:          aws.String("e2e.aws"),
					Value:        aws.String("true"),
					ResourceId:   aws.String(autoScalingGroupMatch),
					ResourceType: aws.String("auto-scaling-group"),
				},
				{
					Key:          aws.String("tcc.zone"),
					Value:        aws.String("other-zone"),
					ResourceId:   aws.String(autoScalingGroupOther),
					ResourceType: aws.String("auto-scaling-group"),
				},
				{
					Key:          aws.String("e2e.aws"),
					Value:        aws.String("true"),
					ResourceId:   aws.String(autoScalingGroupOther),
					ResourceType: aws.String("auto-scaling-group"),
				},
			},
		})
		require.NoError(t, err)

		described, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			Filters: []autoscalingtypes.Filter{
				{
					Name:   aws.String("tag:tcc.zone"),
					Values: []string{"e2e-aws-zone"},
				},
				{
					Name:   aws.String("tag:e2e.aws"),
					Values: []string{"true"},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, described.AutoScalingGroups, 1)
		assert.Equal(t, autoScalingGroupMatch, aws.ToString(described.AutoScalingGroups[0].AutoScalingGroupName))
	})
}

func TestAutoScalingGroupCreateWithTags(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-create-tags-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-create-tags-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
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
			Tags: []autoscalingtypes.Tag{
				{
					Key:               aws.String("tcc.zone"),
					Value:             aws.String("e2e-aws-zone"),
					PropagateAtLaunch: aws.Bool(true),
					ResourceId:        aws.String(autoScalingGroupName),
					ResourceType:      aws.String("auto-scaling-group"),
				},
				{
					Key:               aws.String("e2e.aws"),
					Value:             aws.String("true"),
					PropagateAtLaunch: aws.Bool(true),
					ResourceId:        aws.String(autoScalingGroupName),
					ResourceType:      aws.String("auto-scaling-group"),
				},
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupAutoScalingGroup(t, e, autoScalingGroupName)
		})

		describedByName, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []string{autoScalingGroupName},
		})
		require.NoError(t, err)
		require.Len(t, describedByName.AutoScalingGroups, 1)
		tagsByKey := make(map[string]string)
		for _, tag := range describedByName.AutoScalingGroups[0].Tags {
			if tag.Key == nil || tag.Value == nil {
				continue
			}
			tagsByKey[*tag.Key] = *tag.Value
		}
		assert.Equal(t, "e2e-aws-zone", tagsByKey["tcc.zone"])
		assert.Equal(t, "true", tagsByKey["e2e.aws"])

		described, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			Filters: []autoscalingtypes.Filter{
				{
					Name:   aws.String("tag:tcc.zone"),
					Values: []string{"e2e-aws-zone"},
				},
				{
					Name:   aws.String("tag:e2e.aws"),
					Values: []string{"true"},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, described.AutoScalingGroups, 1)
		assert.Equal(t, autoScalingGroupName, aws.ToString(described.AutoScalingGroups[0].AutoScalingGroupName))
	})
}

func TestAutoScalingGroupUsesExplicitLaunchTemplateVersion(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-ver-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-ver-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		versionResp, err := e.Client.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
			LaunchTemplateId: lt.LaunchTemplate.LaunchTemplateId,
			SourceVersion:    aws.String("$Default"),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				InstanceType: ec2types.InstanceTypeA14xlarge,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, versionResp.LaunchTemplateVersion)
		require.NotNil(t, versionResp.LaunchTemplateVersion.VersionNumber)
		assert.Equal(t, int64(2), *versionResp.LaunchTemplateVersion.VersionNumber)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(1),
			DesiredCapacity:      aws.Int32(1),
			LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
				LaunchTemplateId: lt.LaunchTemplate.LaunchTemplateId,
				Version:          aws.String("2"),
			},
		})
		require.NoError(t, err)

		t.Cleanup(func() {
			cleanupAutoScalingGroup(t, e, autoScalingGroupName)
		})

		describeResp, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []string{autoScalingGroupName},
		})
		require.NoError(t, err)
		require.Len(t, describeResp.AutoScalingGroups, 1)

		group := describeResp.AutoScalingGroups[0]
		require.NotNil(t, group.LaunchTemplate)
		require.NotNil(t, group.LaunchTemplate.Version)
		assert.Equal(t, "2", *group.LaunchTemplate.Version)

		require.Len(t, group.Instances, 1)
		require.NotNil(t, group.Instances[0].InstanceType)
		assert.Equal(t, string(ec2types.InstanceTypeA14xlarge), *group.Instances[0].InstanceType)
	})
}

func TestAutoScalingGroupLaunchTemplateUserDataAppliedToInstances(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		userData := "#!/bin/sh\necho from-launch-template\n"
		encodedUserData := base64.StdEncoding.EncodeToString([]byte(userData))
		launchTemplateName := fmt.Sprintf("lt-asg-user-data-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-user-data-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
				UserData:     aws.String(encodedUserData),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(2),
			DesiredCapacity:      aws.Int32(2),
			LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
				LaunchTemplateId: lt.LaunchTemplate.LaunchTemplateId,
				Version:          aws.String("$Default"),
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupAutoScalingGroup(t, e, autoScalingGroupName)
		})

		require.Eventually(t, func() bool {
			describeCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(describeCtx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 2 {
				return false
			}

			for _, instance := range group.Instances {
				if instance.InstanceId == nil {
					return false
				}
				containerID := strings.TrimPrefix(*instance.InstanceId, "i-")
				inspectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				out, err := dockerCommandContext(
					inspectCtx,
					e.DockerHost,
					"inspect",
					"--format",
					"{{ json .Config.Labels }}",
					containerID,
				).CombinedOutput()
				cancel()
				if err != nil {
					return false
				}
				labels := map[string]string{}
				if err := json.Unmarshal(bytes.TrimSpace(out), &labels); err != nil {
					return false
				}
				if labels["dc2:user-data"] != userData {
					return false
				}
			}
			return true
		}, 15*time.Second, 250*time.Millisecond)
	})
}

func TestAutoScalingGroupLaunchTemplateBlockDeviceMappings(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		deviceName := "/dev/sdf"
		launchTemplateName := fmt.Sprintf("lt-asg-bdm-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-bdm-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
				BlockDeviceMappings: []ec2types.LaunchTemplateBlockDeviceMappingRequest{
					{
						DeviceName: aws.String(deviceName),
						Ebs: &ec2types.LaunchTemplateEbsBlockDeviceRequest{
							DeleteOnTermination: aws.Bool(true),
							VolumeSize:          aws.Int32(1),
							VolumeType:          ec2types.VolumeTypeGp3,
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(1),
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

		var instanceID string
		var volumeID string
		require.Eventually(t, func() bool {
			groupOut, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(groupOut.AutoScalingGroups) != 1 {
				return false
			}
			group := groupOut.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			instanceID = *group.Instances[0].InstanceId

			volumesOut, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{})
			if err != nil {
				return false
			}
			for _, volume := range volumesOut.Volumes {
				if volume.VolumeId == nil || volume.Size == nil || *volume.Size != 1 {
					continue
				}
				if len(volume.Attachments) != 1 {
					continue
				}
				attachment := volume.Attachments[0]
				if attachment.InstanceId == nil || attachment.Device == nil {
					continue
				}
				if *attachment.InstanceId == instanceID && *attachment.Device == deviceName {
					volumeID = *volume.VolumeId
					return true
				}
			}
			return false
		}, 20*time.Second, 250*time.Millisecond)
		require.NotEmpty(t, volumeID)

		_, err = e.AutoScalingClient.UpdateAutoScalingGroup(ctx, &autoscaling.UpdateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(0),
			DesiredCapacity:      aws.Int32(0),
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			groupOut, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			return err == nil &&
				len(groupOut.AutoScalingGroups) == 1 &&
				len(groupOut.AutoScalingGroups[0].Instances) == 0
		}, 20*time.Second, 250*time.Millisecond)

		require.Eventually(t, func() bool {
			volumesOut, err := e.Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{})
			if err != nil {
				return false
			}
			for _, volume := range volumesOut.Volumes {
				if volume.VolumeId != nil && *volume.VolumeId == volumeID {
					return false
				}
			}
			return true
		}, 20*time.Second, 250*time.Millisecond)
	})
}

func TestAutoScalingGroupDetachInstancesReplacesInstance(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-detach-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-detach-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(3),
			DesiredCapacity:      aws.Int32(2),
			LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
				LaunchTemplateId: lt.LaunchTemplate.LaunchTemplateId,
				Version:          aws.String("$Default"),
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupAutoScalingGroup(t, e, autoScalingGroupName)
		})

		initialGroup, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []string{autoScalingGroupName},
		})
		require.NoError(t, err)
		require.Len(t, initialGroup.AutoScalingGroups, 1)
		require.Len(t, initialGroup.AutoScalingGroups[0].Instances, 2)
		detachedInstanceID := aws.ToString(initialGroup.AutoScalingGroups[0].Instances[0].InstanceId)
		initialInstanceIDs := map[string]struct{}{}
		for _, instance := range initialGroup.AutoScalingGroups[0].Instances {
			require.NotNil(t, instance.InstanceId)
			initialInstanceIDs[*instance.InstanceId] = struct{}{}
		}
		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{detachedInstanceID},
			})
			if err != nil && !isInstanceNotFound(err) {
				t.Logf("cleanup terminate detached instance %s returned error: %v", detachedInstanceID, err)
			}
		})

		_, err = e.AutoScalingClient.DetachInstances(ctx, &autoscaling.DetachInstancesInput{
			AutoScalingGroupName:           aws.String(autoScalingGroupName),
			InstanceIds:                    []string{detachedInstanceID},
			ShouldDecrementDesiredCapacity: aws.Bool(false),
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if group.DesiredCapacity == nil || *group.DesiredCapacity != 2 {
				return false
			}
			if len(group.Instances) != 2 {
				return false
			}
			groupInstanceIDs := make([]string, 0, len(group.Instances))
			for _, instance := range group.Instances {
				if instance.InstanceId == nil {
					return false
				}
				groupInstanceIDs = append(groupInstanceIDs, *instance.InstanceId)
			}
			if slices.Contains(groupInstanceIDs, detachedInstanceID) {
				return false
			}
			for _, id := range groupInstanceIDs {
				if _, found := initialInstanceIDs[id]; !found {
					return true
				}
			}
			return false
		}, 15*time.Second, 250*time.Millisecond)

		detachedOut, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{detachedInstanceID},
		})
		require.NoError(t, err)
		require.Len(t, detachedOut.Reservations, 1)
		require.Len(t, detachedOut.Reservations[0].Instances, 1)
	})
}

func TestAutoScalingGroupReplacesOutOfBandDeletedInstance(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-oob-delete-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-oob-delete-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(1),
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

		var deletedInstanceID string
		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			deletedInstanceID = *group.Instances[0].InstanceId
			return true
		}, 20*time.Second, 250*time.Millisecond)
		require.NotEmpty(t, deletedInstanceID)

		containerID := strings.TrimPrefix(deletedInstanceID, "i-")
		rmOut, rmErr := dockerCommandContext(ctx, e.DockerHost, "rm", "-f", containerID).CombinedOutput()
		require.NoError(t, rmErr, "docker rm output: %s", string(rmOut))

		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			return *group.Instances[0].InstanceId != deletedInstanceID
		}, 20*time.Second, 250*time.Millisecond)
	})
}

func TestAutoScalingGroupReplacesOutOfBandDeletedInstanceOnEC2Describe(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-oob-delete-ec2-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-oob-delete-ec2-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(1),
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

		var deletedInstanceID string
		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			deletedInstanceID = *group.Instances[0].InstanceId
			return true
		}, 20*time.Second, 250*time.Millisecond)
		require.NotEmpty(t, deletedInstanceID)

		containerID := strings.TrimPrefix(deletedInstanceID, "i-")
		rmOut, rmErr := dockerCommandContext(ctx, e.DockerHost, "rm", "-f", containerID).CombinedOutput()
		require.NoError(t, rmErr, "docker rm output: %s", string(rmOut))

		require.Eventually(t, func() bool {
			out, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
			if err != nil {
				return false
			}
			instanceIDs := make([]string, 0)
			for _, reservation := range out.Reservations {
				for _, instance := range reservation.Instances {
					if instance.InstanceId == nil {
						continue
					}
					instanceIDs = append(instanceIDs, *instance.InstanceId)
				}
			}
			if len(instanceIDs) != 1 {
				return false
			}
			return instanceIDs[0] != deletedInstanceID
		}, 20*time.Second, 250*time.Millisecond)
	})
}

func TestAutoScalingGroupReplacesOutOfBandStoppedInstance(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-asg-oob-stop-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-oob-stop-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(1),
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

		var stoppedInstanceID string
		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			stoppedInstanceID = *group.Instances[0].InstanceId
			return true
		}, 20*time.Second, 250*time.Millisecond)
		require.NotEmpty(t, stoppedInstanceID)

		containerID := strings.TrimPrefix(stoppedInstanceID, "i-")
		stopOut, stopErr := dockerCommandContext(ctx, e.DockerHost, "stop", containerID).CombinedOutput()
		require.NoError(t, stopErr, "docker stop output: %s", string(stopOut))

		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			return *group.Instances[0].InstanceId != stoppedInstanceID
		}, 20*time.Second, 250*time.Millisecond)
	})
}

func TestAutoScalingGroupReplacesUnhealthyInstance(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		imageName := buildASGHealthCheckTestImage(t, ctx, e.DockerHost)
		launchTemplateName := fmt.Sprintf("lt-asg-health-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		autoScalingGroupName := fmt.Sprintf("asg-health-%s", strings.ReplaceAll(t.Name(), "/", "-"))

		lt, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String(imageName),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, lt.LaunchTemplate)
		require.NotNil(t, lt.LaunchTemplate.LaunchTemplateId)

		_, err = e.AutoScalingClient.CreateAutoScalingGroup(ctx, &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: aws.String(autoScalingGroupName),
			MinSize:              aws.Int32(1),
			MaxSize:              aws.Int32(1),
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

		var unhealthyInstanceID string
		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			unhealthyInstanceID = *group.Instances[0].InstanceId
			return true
		}, 20*time.Second, 250*time.Millisecond)
		require.NotEmpty(t, unhealthyInstanceID)

		containerID := strings.TrimPrefix(unhealthyInstanceID, "i-")
		failOut, failErr := dockerCommandContext(
			ctx,
			e.DockerHost,
			"exec",
			containerID,
			"sh",
			"-ceu",
			"rm -f /tmp/dc2-health",
		).CombinedOutput()
		require.NoError(t, failErr, "docker exec output: %s", string(failOut))

		require.Eventually(t, func() bool {
			out, err := e.AutoScalingClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
				AutoScalingGroupNames: []string{autoScalingGroupName},
			})
			if err != nil || len(out.AutoScalingGroups) != 1 {
				return false
			}
			group := out.AutoScalingGroups[0]
			if len(group.Instances) != 1 || group.Instances[0].InstanceId == nil {
				return false
			}
			return *group.Instances[0].InstanceId != unhealthyInstanceID
		}, 30*time.Second, 250*time.Millisecond)
	})
}

func buildASGHealthCheckTestImage(t *testing.T, ctx context.Context, dockerHost string) string {
	t.Helper()

	imageName := fmt.Sprintf(
		"dc2-asg-healthcheck-%d:%d",
		time.Now().UnixNano(),
		time.Now().UnixNano(),
	)
	buildDir := t.TempDir()
	dockerfilePath := filepath.Join(buildDir, "Dockerfile")
	dockerfile := `FROM nginx:alpine
RUN touch /tmp/dc2-health
HEALTHCHECK --interval=1s --timeout=1s --retries=2 CMD test -f /tmp/dc2-health
`
	require.NoError(t, os.WriteFile(dockerfilePath, []byte(dockerfile), 0o644))

	buildOut, buildErr := dockerCommandContext(ctx, dockerHost, "build", "-t", imageName, buildDir).CombinedOutput()
	require.NoError(t, buildErr, "docker build output: %s", string(buildOut))

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		removeOut, removeErr := dockerCommandContext(cleanupCtx, dockerHost, "rmi", "-f", imageName).CombinedOutput()
		if removeErr != nil {
			t.Logf("cleanup remove test image %s failed: %v output: %s", imageName, removeErr, string(removeOut))
		}
	})

	return imageName
}

func cleanupAutoScalingGroup(t *testing.T, e *TestEnvironment, autoScalingGroupName string) {
	t.Helper()

	cleanupCtx, cancel := cleanupAPICtx(t)
	defer cancel()

	_, err := e.AutoScalingClient.DeleteAutoScalingGroup(cleanupCtx, &autoscaling.DeleteAutoScalingGroupInput{
		AutoScalingGroupName: aws.String(autoScalingGroupName),
		ForceDelete:          aws.Bool(true),
	})
	if err != nil && !isAutoScalingGroupNotFound(err) {
		require.NoError(t, err, "cleanup delete autoscaling group %s returned error", autoScalingGroupName)
	}

	require.Eventually(t, func() bool {
		describeCtx, cancel := cleanupAPICtx(t)
		defer cancel()
		out, err := e.AutoScalingClient.DescribeAutoScalingGroups(describeCtx, &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []string{autoScalingGroupName},
		})
		if err != nil {
			t.Logf("describe autoscaling group during cleanup failed: %v", err)
			return false
		}
		return len(out.AutoScalingGroups) == 0
	}, 10*time.Second, 250*time.Millisecond, "autoscaling group %s was not deleted", autoScalingGroupName)
}

func isAutoScalingGroupNotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.ErrorCode() != "ValidationError" {
		return false
	}
	return strings.Contains(apiErr.ErrorMessage(), "was not found")
}

func isInstanceNotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.ErrorCode() == "InvalidInstanceID.NotFound"
}
