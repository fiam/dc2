package dc2_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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
