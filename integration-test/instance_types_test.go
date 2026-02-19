package dc2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribeInstanceTypes(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		input := &ec2.DescribeInstanceTypesInput{MaxResults: aws.Int32(1)}
		resp, err := e.Client.DescribeInstanceTypes(ctx, input)
		require.NoError(t, err)

		if resp.NextToken != nil {
			nextResp, err := e.Client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
				MaxResults: aws.Int32(1),
				NextToken:  resp.NextToken,
			})
			require.NoError(t, err)
			assert.NotEqual(t, aws.ToString(resp.NextToken), aws.ToString(nextResp.NextToken))
		}
	})
}

func TestDescribeInstanceTypeOfferingsGlobalAvailability(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		respEU, err := e.Client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: "region",
			Filters: []ec2types.Filter{{
				Name:   aws.String("location"),
				Values: []string{"eu-west-1"},
			}},
		})
		require.NoError(t, err)
		for _, offering := range respEU.InstanceTypeOfferings {
			assert.Equal(t, "eu-west-1", aws.ToString(offering.Location))
			assert.Equal(t, "region", string(offering.LocationType))
		}

		respAP, err := e.Client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: "region",
			Filters: []ec2types.Filter{{
				Name:   aws.String("location"),
				Values: []string{"ap-south-1"},
			}},
		})
		require.NoError(t, err)
		assert.Equal(t, len(respEU.InstanceTypeOfferings), len(respAP.InstanceTypeOfferings))
	})
}

func TestGetInstanceTypesFromInstanceRequirements(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		resp, err := e.Client.GetInstanceTypesFromInstanceRequirements(ctx, &ec2.GetInstanceTypesFromInstanceRequirementsInput{
			ArchitectureTypes: []ec2types.ArchitectureType{ec2types.ArchitectureTypeArm64, ec2types.ArchitectureTypeX8664},
			VirtualizationTypes: []ec2types.VirtualizationType{
				ec2types.VirtualizationTypeHvm,
			},
			InstanceRequirements: &ec2types.InstanceRequirementsRequest{
				VCpuCount: &ec2types.VCpuCountRangeRequest{Min: aws.Int32(1), Max: aws.Int32(256)},
				MemoryMiB: &ec2types.MemoryMiBRequest{Min: aws.Int32(1), Max: aws.Int32(1048576)},
			},
		})
		require.NoError(t, err)

		for _, instanceType := range resp.InstanceTypes {
			assert.NotEmpty(t, aws.ToString(instanceType.InstanceType))
		}
	})
}
