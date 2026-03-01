package dc2_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLaunchTemplateBadRequests(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		t.Run("invalid tag specification", func(t *testing.T) {
			t.Parallel()
			resp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
				LaunchTemplateName: aws.String("test-launch-template"),
				LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
					TagSpecifications: []ec2types.LaunchTemplateTagSpecificationRequest{
						{
							ResourceType: ec2types.ResourceTypeLaunchTemplate, // must be instance | volume | network-interface | spot-instances-request
							Tags: []ec2types.Tag{
								{
									Key:   aws.String("Name"),
									Value: aws.String("test-instance"),
								},
							},
						},
					},
				},
			})
			require.Error(t, err)
			require.Nil(t, resp)
		})
	})
}

func TestLaunchTemplateDescribeDelete(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		createResp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, createResp.LaunchTemplate)
		require.NotNil(t, createResp.LaunchTemplate.LaunchTemplateId)
		launchTemplateID := *createResp.LaunchTemplate.LaunchTemplateId

		describeByIDResp, err := e.Client.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
			LaunchTemplateIds: []string{launchTemplateID},
		})
		require.NoError(t, err)
		require.Len(t, describeByIDResp.LaunchTemplates, 1)
		require.NotNil(t, describeByIDResp.LaunchTemplates[0].LaunchTemplateId)
		assert.Equal(t, launchTemplateID, *describeByIDResp.LaunchTemplates[0].LaunchTemplateId)
		require.NotNil(t, describeByIDResp.LaunchTemplates[0].LaunchTemplateName)
		assert.Equal(t, launchTemplateName, *describeByIDResp.LaunchTemplates[0].LaunchTemplateName)

		describeByNameResp, err := e.Client.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
			LaunchTemplateNames: []string{launchTemplateName},
		})
		require.NoError(t, err)
		require.Len(t, describeByNameResp.LaunchTemplates, 1)
		require.NotNil(t, describeByNameResp.LaunchTemplates[0].LaunchTemplateId)
		assert.Equal(t, launchTemplateID, *describeByNameResp.LaunchTemplates[0].LaunchTemplateId)

		deleteResp, err := e.Client.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
		})
		require.NoError(t, err)
		require.NotNil(t, deleteResp.LaunchTemplate)
		require.NotNil(t, deleteResp.LaunchTemplate.LaunchTemplateId)
		assert.Equal(t, launchTemplateID, *deleteResp.LaunchTemplate.LaunchTemplateId)

		describeAfterDeleteResp, err := e.Client.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
			LaunchTemplateIds: []string{launchTemplateID},
		})
		require.NoError(t, err)
		require.Empty(t, describeAfterDeleteResp.LaunchTemplates)
	})
}

func TestLaunchTemplateVersions(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		userData := "#!/bin/sh\necho launch-template\n"
		encodedUserData := base64.StdEncoding.EncodeToString([]byte(userData))
		deviceName := "/dev/sdf"
		launchTemplateName := fmt.Sprintf("lt-ver-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		createResp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
				UserData:     aws.String(encodedUserData),
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
		require.NotNil(t, createResp.LaunchTemplate)
		require.NotNil(t, createResp.LaunchTemplate.LaunchTemplateId)
		launchTemplateID := *createResp.LaunchTemplate.LaunchTemplateId

		createVersionResp, err := e.Client.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
			LaunchTemplateId:   aws.String(launchTemplateID),
			SourceVersion:      aws.String("$Default"),
			VersionDescription: aws.String("switch instance type"),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				InstanceType: ec2types.InstanceTypeA14xlarge,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, createVersionResp.LaunchTemplateVersion)
		require.NotNil(t, createVersionResp.LaunchTemplateVersion.VersionNumber)
		assert.Equal(t, int64(2), *createVersionResp.LaunchTemplateVersion.VersionNumber)
		require.NotNil(t, createVersionResp.LaunchTemplateVersion.DefaultVersion)
		assert.False(t, *createVersionResp.LaunchTemplateVersion.DefaultVersion)

		describeVersionsResp, err := e.Client.DescribeLaunchTemplateVersions(ctx, &ec2.DescribeLaunchTemplateVersionsInput{
			LaunchTemplateId: aws.String(launchTemplateID),
		})
		require.NoError(t, err)
		require.Len(t, describeVersionsResp.LaunchTemplateVersions, 2)

		versionsByNumber := map[int64]ec2types.LaunchTemplateVersion{}
		for _, v := range describeVersionsResp.LaunchTemplateVersions {
			require.NotNil(t, v.VersionNumber)
			versionsByNumber[*v.VersionNumber] = v
		}

		v1, ok := versionsByNumber[1]
		require.True(t, ok)
		require.NotNil(t, v1.DefaultVersion)
		assert.True(t, *v1.DefaultVersion)
		require.NotNil(t, v1.LaunchTemplateData)
		require.NotNil(t, v1.LaunchTemplateData.InstanceType)
		assert.Equal(t, ec2types.InstanceTypeA1Large, v1.LaunchTemplateData.InstanceType)
		require.NotNil(t, v1.LaunchTemplateData.UserData)
		assert.Equal(t, encodedUserData, *v1.LaunchTemplateData.UserData)
		require.Len(t, v1.LaunchTemplateData.BlockDeviceMappings, 1)
		require.NotNil(t, v1.LaunchTemplateData.BlockDeviceMappings[0].DeviceName)
		assert.Equal(t, deviceName, *v1.LaunchTemplateData.BlockDeviceMappings[0].DeviceName)
		require.NotNil(t, v1.LaunchTemplateData.BlockDeviceMappings[0].Ebs)
		require.NotNil(t, v1.LaunchTemplateData.BlockDeviceMappings[0].Ebs.DeleteOnTermination)
		assert.True(t, *v1.LaunchTemplateData.BlockDeviceMappings[0].Ebs.DeleteOnTermination)
		require.NotNil(t, v1.LaunchTemplateData.BlockDeviceMappings[0].Ebs.VolumeSize)
		assert.Equal(t, int32(1), *v1.LaunchTemplateData.BlockDeviceMappings[0].Ebs.VolumeSize)
		assert.Equal(t, ec2types.VolumeTypeGp3, v1.LaunchTemplateData.BlockDeviceMappings[0].Ebs.VolumeType)

		v2, ok := versionsByNumber[2]
		require.True(t, ok)
		require.NotNil(t, v2.DefaultVersion)
		assert.False(t, *v2.DefaultVersion)
		require.NotNil(t, v2.LaunchTemplateData)
		require.NotNil(t, v2.LaunchTemplateData.InstanceType)
		assert.Equal(t, ec2types.InstanceTypeA14xlarge, v2.LaunchTemplateData.InstanceType)
		require.NotNil(t, v2.LaunchTemplateData.UserData)
		assert.Equal(t, encodedUserData, *v2.LaunchTemplateData.UserData)
		require.Len(t, v2.LaunchTemplateData.BlockDeviceMappings, 1)
		require.NotNil(t, v2.LaunchTemplateData.BlockDeviceMappings[0].DeviceName)
		assert.Equal(t, deviceName, *v2.LaunchTemplateData.BlockDeviceMappings[0].DeviceName)
		require.NotNil(t, v2.LaunchTemplateData.BlockDeviceMappings[0].Ebs)
		require.NotNil(t, v2.LaunchTemplateData.BlockDeviceMappings[0].Ebs.DeleteOnTermination)
		assert.True(t, *v2.LaunchTemplateData.BlockDeviceMappings[0].Ebs.DeleteOnTermination)
		require.NotNil(t, v2.LaunchTemplateData.BlockDeviceMappings[0].Ebs.VolumeSize)
		assert.Equal(t, int32(1), *v2.LaunchTemplateData.BlockDeviceMappings[0].Ebs.VolumeSize)
		assert.Equal(t, ec2types.VolumeTypeGp3, v2.LaunchTemplateData.BlockDeviceMappings[0].Ebs.VolumeType)
		require.NotNil(t, v2.VersionDescription)
		assert.Equal(t, "switch instance type", *v2.VersionDescription)

		modifyResp, err := e.Client.ModifyLaunchTemplate(ctx, &ec2.ModifyLaunchTemplateInput{
			LaunchTemplateId: aws.String(launchTemplateID),
			DefaultVersion:   aws.String("2"),
		})
		require.NoError(t, err)
		require.NotNil(t, modifyResp.LaunchTemplate)
		require.NotNil(t, modifyResp.LaunchTemplate.DefaultVersionNumber)
		assert.Equal(t, int64(2), *modifyResp.LaunchTemplate.DefaultVersionNumber)

		describeResp, err := e.Client.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
			LaunchTemplateIds: []string{launchTemplateID},
		})
		require.NoError(t, err)
		require.Len(t, describeResp.LaunchTemplates, 1)
		require.NotNil(t, describeResp.LaunchTemplates[0].DefaultVersionNumber)
		assert.Equal(t, int64(2), *describeResp.LaunchTemplates[0].DefaultVersionNumber)
		require.NotNil(t, describeResp.LaunchTemplates[0].LatestVersionNumber)
		assert.Equal(t, int64(2), *describeResp.LaunchTemplates[0].LatestVersionNumber)

		describeDefaultVersionResp, err := e.Client.DescribeLaunchTemplateVersions(ctx, &ec2.DescribeLaunchTemplateVersionsInput{
			LaunchTemplateId: aws.String(launchTemplateID),
			Versions:         []string{"$Default"},
		})
		require.NoError(t, err)
		require.Len(t, describeDefaultVersionResp.LaunchTemplateVersions, 1)
		require.NotNil(t, describeDefaultVersionResp.LaunchTemplateVersions[0].VersionNumber)
		assert.Equal(t, int64(2), *describeDefaultVersionResp.LaunchTemplateVersions[0].VersionNumber)
		require.NotNil(t, describeDefaultVersionResp.LaunchTemplateVersions[0].DefaultVersion)
		assert.True(t, *describeDefaultVersionResp.LaunchTemplateVersions[0].DefaultVersion)
	})
}

func TestLaunchTemplate(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		resp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String("test-launch-template"),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				InstanceType: ec2types.InstanceTypeA14xlarge,
				TagSpecifications: []ec2types.LaunchTemplateTagSpecificationRequest{
					{
						ResourceType: ec2types.ResourceTypeInstance,
						Tags: []ec2types.Tag{
							{
								Key:   aws.String("Name"),
								Value: aws.String("test-instance"),
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.LaunchTemplate.LaunchTemplateId)
	})
}

func TestRunInstancesWithLaunchTemplateDefaultVersion(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-run-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		createResp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, createResp.LaunchTemplate)
		require.NotNil(t, createResp.LaunchTemplate.LaunchTemplateId)

		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			LaunchTemplate: &ec2types.LaunchTemplateSpecification{
				LaunchTemplateId: createResp.LaunchTemplate.LaunchTemplateId,
				Version:          aws.String("$Default"),
			},
			MinCount: aws.Int32(1),
			MaxCount: aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runResp.Instances, 1)

		instance := runResp.Instances[0]
		require.NotNil(t, instance.InstanceId)
		assert.Equal(t, "nginx", aws.ToString(instance.ImageId))
		assert.Equal(t, ec2types.InstanceTypeA1Large, instance.InstanceType)

		t.Cleanup(func() {
			apiCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, terminateErr := e.Client.TerminateInstances(apiCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{aws.ToString(instance.InstanceId)},
			})
			require.NoError(t, terminateErr)
		})
	})
}

func TestRunInstancesLaunchTemplateAllowsFieldOverrides(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-override-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		launchTemplateUserData := "#!/bin/sh\necho launch-template\n"
		overrideUserData := "#!/bin/sh\necho override\n"
		createResp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
				UserData:     aws.String(base64.StdEncoding.EncodeToString([]byte(launchTemplateUserData))),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, createResp.LaunchTemplate)
		require.NotNil(t, createResp.LaunchTemplate.LaunchTemplateId)

		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx:alpine"),
			InstanceType: ec2types.InstanceTypeA14xlarge,
			UserData:     aws.String(base64.StdEncoding.EncodeToString([]byte(overrideUserData))),
			LaunchTemplate: &ec2types.LaunchTemplateSpecification{
				LaunchTemplateId: createResp.LaunchTemplate.LaunchTemplateId,
				Version:          aws.String("$Default"),
			},
			MinCount: aws.Int32(1),
			MaxCount: aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runResp.Instances, 1)
		require.NotNil(t, runResp.Instances[0].InstanceId)
		assert.Equal(t, "nginx:alpine", aws.ToString(runResp.Instances[0].ImageId))
		assert.Equal(t, ec2types.InstanceTypeA14xlarge, runResp.Instances[0].InstanceType)

		instanceID := aws.ToString(runResp.Instances[0].InstanceId)
		containerID := containerIDForInstanceID(t, ctx, e.DockerHost, instanceID)
		userDataLabelOutput, err := dockerCommandContext(
			ctx,
			e.DockerHost,
			"inspect",
			"-f",
			`{{index .Config.Labels "dc2:user-data"}}`,
			containerID,
		).CombinedOutput()
		require.NoError(t, err, "docker inspect user-data label output: %s", string(userDataLabelOutput))
		assert.Equal(t, strings.TrimSpace(overrideUserData), strings.TrimSpace(string(userDataLabelOutput)))

		t.Cleanup(func() {
			apiCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, terminateErr := e.Client.TerminateInstances(apiCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, terminateErr)
		})
	})
}
