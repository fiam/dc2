package dc2_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
			_, _ = e.AutoScalingClient.DeleteAutoScalingGroup(ctx, &autoscaling.DeleteAutoScalingGroupInput{
				AutoScalingGroupName: aws.String(autoScalingGroupName),
				ForceDelete:          aws.Bool(true),
			})
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
