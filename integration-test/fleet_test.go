package dc2_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateFleetFromLaunchTemplateInstanceRequirements(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-fleet-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		createResp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId: aws.String("nginx"),
				InstanceRequirements: &ec2types.InstanceRequirementsRequest{
					VCpuCount: &ec2types.VCpuCountRangeRequest{
						Min: aws.Int32(2),
						Max: aws.Int32(2),
					},
					MemoryMiB: &ec2types.MemoryMiBRequest{
						Min: aws.Int32(4096),
						Max: aws.Int32(4096),
					},
					AllowedInstanceTypes: []string{"a1.large"},
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, createResp.LaunchTemplate)

		fleetResp, err := e.Client.CreateFleet(ctx, &ec2.CreateFleetInput{
			Type: ec2types.FleetTypeInstant,
			TargetCapacitySpecification: &ec2types.TargetCapacitySpecificationRequest{
				TotalTargetCapacity:       aws.Int32(1),
				DefaultTargetCapacityType: ec2types.DefaultTargetCapacityTypeOnDemand,
			},
			LaunchTemplateConfigs: []ec2types.FleetLaunchTemplateConfigRequest{
				{
					LaunchTemplateSpecification: &ec2types.FleetLaunchTemplateSpecificationRequest{
						LaunchTemplateId: createResp.LaunchTemplate.LaunchTemplateId,
						Version:          aws.String("$Default"),
					},
					Overrides: []ec2types.FleetLaunchTemplateOverridesRequest{
						{
							SubnetId:         aws.String("subnet-fleet"),
							AvailabilityZone: aws.String("us-east-1b"),
							Placement: &ec2types.Placement{
								GroupName: aws.String("pg-fleet"),
							},
						},
					},
				},
			},
			TagSpecifications: []ec2types.TagSpecification{
				{
					ResourceType: ec2types.ResourceTypeInstance,
					Tags: []ec2types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String("spawned-fleet"),
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, fleetResp.Instances, 1)
		require.Len(t, fleetResp.Instances[0].InstanceIds, 1)
		assert.Equal(t, ec2types.InstanceTypeA1Large, fleetResp.Instances[0].InstanceType)
		require.NotNil(t, fleetResp.Instances[0].LaunchTemplateAndOverrides)
		require.NotNil(t, fleetResp.Instances[0].LaunchTemplateAndOverrides.LaunchTemplateSpecification)
		require.NotNil(t, fleetResp.Instances[0].LaunchTemplateAndOverrides.Overrides)
		assert.Equal(
			t,
			aws.ToString(createResp.LaunchTemplate.LaunchTemplateId),
			aws.ToString(fleetResp.Instances[0].LaunchTemplateAndOverrides.LaunchTemplateSpecification.LaunchTemplateId),
		)
		assert.Equal(
			t,
			"subnet-fleet",
			aws.ToString(fleetResp.Instances[0].LaunchTemplateAndOverrides.Overrides.SubnetId),
		)
		assert.Equal(
			t,
			"us-east-1b",
			aws.ToString(fleetResp.Instances[0].LaunchTemplateAndOverrides.Overrides.AvailabilityZone),
		)

		instanceID := fleetResp.Instances[0].InstanceIds[0]
		t.Cleanup(func() {
			apiCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, terminateErr := e.Client.TerminateInstances(apiCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, terminateErr)
		})

		describeResp, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)
		require.Len(t, describeResp.Reservations, 1)
		require.Len(t, describeResp.Reservations[0].Instances, 1)

		instance := describeResp.Reservations[0].Instances[0]
		assert.Equal(t, ec2types.InstanceTypeA1Large, instance.InstanceType)
		assert.Equal(t, "subnet-fleet", aws.ToString(instance.SubnetId))
		assert.Equal(t, "us-east-1b", aws.ToString(instance.Placement.AvailabilityZone))

		tagsByKey := make(map[string]string)
		for _, tag := range instance.Tags {
			if tag.Key == nil || tag.Value == nil {
				continue
			}
			tagsByKey[*tag.Key] = *tag.Value
		}
		assert.Equal(t, "spawned-fleet", tagsByKey["Name"])
		assert.Equal(t, aws.ToString(createResp.LaunchTemplate.LaunchTemplateId), tagsByKey["aws:ec2launchtemplate:id"])
		assert.Equal(t, "1", tagsByKey["aws:ec2launchtemplate:version"])
	})
}

func TestCreateFleetRejectsNonInstantType(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		launchTemplateName := fmt.Sprintf("lt-fleet-invalid-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		createResp, err := e.Client.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: aws.String(launchTemplateName),
			LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
				ImageId:      aws.String("nginx"),
				InstanceType: ec2types.InstanceTypeA1Large,
			},
		})
		require.NoError(t, err)

		resp, err := e.Client.CreateFleet(ctx, &ec2.CreateFleetInput{
			Type: ec2types.FleetTypeMaintain,
			TargetCapacitySpecification: &ec2types.TargetCapacitySpecificationRequest{
				TotalTargetCapacity: aws.Int32(1),
			},
			LaunchTemplateConfigs: []ec2types.FleetLaunchTemplateConfigRequest{
				{
					LaunchTemplateSpecification: &ec2types.FleetLaunchTemplateSpecificationRequest{
						LaunchTemplateId: createResp.LaunchTemplate.LaunchTemplateId,
						Version:          aws.String("$Default"),
					},
				},
			},
		})
		require.Error(t, err)
		require.Nil(t, resp)
	})
}
