package format

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
)

type inner2 struct {
	Field2 string `url:"field2"`
	Field3 int    `url:"field3"`
}

type inner1 struct {
	Field1 string   `url:"field1"`
	Inners []inner2 `url:"inners"`
}

type outer struct {
	Inner inner1 `url:"inner"`
}

type embedded struct {
	outer
}

type outerWithStrings struct {
	Strings []string `url:"strings"`
}

type innerPtr struct {
	Field1 string `url:"field1"`
	Field2 string `url:"field2"`
}

type outerWithInnerPtr struct {
	Inner *innerPtr `url:"inner"`
}

type autoScalingFilter struct {
	Name   string   `url:"Name"`
	Values []string `url:"Values"`
}

type describeAutoScalingGroups struct {
	Filters []autoScalingFilter `url:"Filters"`
}

func TestDecodeURLEncoded(t *testing.T) {
	t.Parallel()
	intPtr := func(v int) *int { return &v }
	// Test cases
	tests := []struct {
		name        string
		values      url.Values
		output      any
		expected    any
		expectedErr error
	}{
		{
			name: "simple",
			values: url.Values{
				"inner.field1":          {"value1"},
				"inner.inners.1.field2": {"value2"},
				"inner.inners.1.field3": {"3"},
			},
			output: &outer{},
			expected: &outer{
				Inner: inner1{
					Field1: "value1",
					Inners: []inner2{
						{
							Field2: "value2",
							Field3: 3,
						},
					},
				},
			},
		},
		{
			name: "zero indexed",
			values: url.Values{
				"inner.inners.0.field2": {"value2"},
			},
			output:      &outer{},
			expectedErr: errZeroIndex,
		},
		{
			name: "negative indexed",
			values: url.Values{
				"inner.inners.-42.field2": {"value2"},
			},
			output:      &outer{},
			expectedErr: errZeroIndex,
		},
		{
			name: "no such field",
			values: url.Values{
				"foo": {"bar"},
			},
			output:      &outer{},
			expectedErr: errNoSuchField,
		},
		{
			name: "embedded",
			values: url.Values{
				"inner.field1":          {"value1"},
				"inner.inners.1.field2": {"value2"},
				"inner.inners.1.field3": {"3"},
			},
			output: &embedded{},
			expected: &embedded{
				outer: outer{
					Inner: inner1{
						Field1: "value1",
						Inners: []inner2{
							{
								Field2: "value2",
								Field3: 3,
							},
						},
					},
				},
			},
		},
		{
			name: "slice of strings",
			values: url.Values{
				"strings.1": {"value1"},
				"strings.2": {"value2"},
			},
			output: &outerWithStrings{},
			expected: &outerWithStrings{
				Strings: []string{"value1", "value2"},
			},
		},
		{
			name: "out of order",
			values: url.Values{
				"strings.1":  {"value1"},
				"strings.2":  {"value2"},
				"strings.3":  {"value3"},
				"strings.5":  {"value5"},
				"strings.4":  {"value4"},
				"strings.6":  {"value6"},
				"strings.7":  {"value7"},
				"strings.8":  {"value8"},
				"strings.9":  {"value9"},
				"strings.10": {"value10"},
			},
			output: &outerWithStrings{},
			expected: &outerWithStrings{
				Strings: []string{"value1", "value2", "value3", "value4", "value5", "value6", "value7", "value8", "value9", "value10"},
			},
		},
		{
			name: "member prefix",
			values: url.Values{
				"strings.member.1": {"value1"},
				"strings.member.2": {"value2"},
			},
			output: &outerWithStrings{},
			expected: &outerWithStrings{
				Strings: []string{"value1", "value2"},
			},
		},
		{
			name: "pointer to struct fields",
			values: url.Values{
				"inner.field1": {"value1"},
				"inner.field2": {"value2"},
			},
			output: &outerWithInnerPtr{},
			expected: &outerWithInnerPtr{
				Inner: &innerPtr{
					Field1: "value1",
					Field2: "value2",
				},
			},
		},
		{
			name: "run instances subnet id",
			values: url.Values{
				"SubnetId": {"subnet-00000000000000000"},
			},
			output: &api.RunInstancesRequest{},
			expected: &api.RunInstancesRequest{
				SubnetID: "subnet-00000000000000000",
			},
		},
		{
			name: "autoscaling filters with values.member",
			values: url.Values{
				"Filters.member.1.Name":            {"tag:tcc.zone"},
				"Filters.member.1.Values.member.1": {"e2e-aws-zone"},
				"Filters.member.2.Name":            {"tag:e2e.aws"},
				"Filters.member.2.Values.member.1": {"true"},
			},
			output: &describeAutoScalingGroups{},
			expected: &describeAutoScalingGroups{
				Filters: []autoScalingFilter{
					{
						Name:   "tag:tcc.zone",
						Values: []string{"e2e-aws-zone"},
					},
					{
						Name:   "tag:e2e.aws",
						Values: []string{"true"},
					},
				},
			},
		},
		{
			name: "launch template instance requirements",
			values: url.Values{
				"LaunchTemplateName":                                    {"lt-abis"},
				"LaunchTemplateData.ImageId":                            {"nginx"},
				"LaunchTemplateData.InstanceRequirements.MemoryMiB.Min": {"4096"},
				"LaunchTemplateData.InstanceRequirements.MemoryMiB.Max": {"8192"},
				"LaunchTemplateData.InstanceRequirements.VCpuCount.Min": {"2"},
				"LaunchTemplateData.InstanceRequirements.VCpuCount.Max": {"4"},
			},
			output: &api.CreateLaunchTemplateRequest{},
			expected: &api.CreateLaunchTemplateRequest{
				LaunchTemplateName: "lt-abis",
				LaunchTemplateData: api.LaunchTemplateData{
					ImageID: "nginx",
					InstanceRequirements: &api.InstanceRequirementsRequest{
						MemoryMiB: &api.IntRangeRequest{
							Min: intPtr(4096),
							Max: intPtr(8192),
						},
						VCPUCount: &api.IntRangeRequest{
							Min: intPtr(2),
							Max: intPtr(4),
						},
					},
				},
			},
		},
		{
			name: "autoscaling mixed instances policy",
			values: url.Values{
				"AutoScalingGroupName": {"asg-abis"},
				"MinSize":              {"0"},
				"MaxSize":              {"2"},
				"DesiredCapacity":      {"1"},
				"VPCZoneIdentifier":    {"subnet-dc2"},
				"MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateId": {"lt-123"},
				"MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.Version":          {"$Default"},
				"MixedInstancesPolicy.InstancesDistribution.OnDemandAllocationStrategy":            {"lowest-price"},
				"MixedInstancesPolicy.InstancesDistribution.OnDemandBaseCapacity":                  {"0"},
				"MixedInstancesPolicy.InstancesDistribution.OnDemandPercentageAboveBaseCapacity":   {"100"},
			},
			output: &api.CreateAutoScalingGroupRequest{},
			expected: &api.CreateAutoScalingGroupRequest{
				AutoScalingGroupName: "asg-abis",
				MinSize:              intPtr(0),
				MaxSize:              intPtr(2),
				DesiredCapacity:      intPtr(1),
				VPCZoneIdentifier:    func(v string) *string { return &v }("subnet-dc2"),
				MixedInstancesPolicy: &api.AutoScalingMixedInstancesPolicy{
					LaunchTemplate: &api.AutoScalingMixedInstancesLaunchTemplate{
						LaunchTemplateSpecification: &api.AutoScalingLaunchTemplateSpecification{
							LaunchTemplateID: func(v string) *string { return &v }("lt-123"),
							Version:          func(v string) *string { return &v }("$Default"),
						},
					},
					InstancesDistribution: &api.AutoScalingMixedInstancesInstancesDistribution{
						OnDemandAllocationStrategy:          func(v string) *string { return &v }("lowest-price"),
						OnDemandBaseCapacity:                intPtr(0),
						OnDemandPercentageAboveBaseCapacity: intPtr(100),
					},
				},
			},
		},
		{
			name: "launch instances request",
			values: url.Values{
				"AutoScalingGroupName":         {"asg-launch"},
				"ClientToken":                  {"token-123"},
				"RequestedCapacity":            {"2"},
				"AvailabilityZoneIds.member.1": {"use1-az1"},
				"SubnetIds.member.1":           {"subnet-dc2"},
				"RetryStrategy":                {"retry-with-group-configuration"},
			},
			output: &api.LaunchInstancesRequest{},
			expected: &api.LaunchInstancesRequest{
				CommonRequest: api.CommonRequest{
					ClientToken: "token-123",
				},
				AutoScalingGroupName: "asg-launch",
				AvailabilityZoneIDs:  []string{"use1-az1"},
				RequestedCapacity:    intPtr(2),
				RetryStrategy:        func(v string) *string { return &v }("retry-with-group-configuration"),
				SubnetIDs:            []string{"subnet-dc2"},
			},
		},
		{
			name: "create fleet request",
			values: url.Values{
				"Type": {"instant"},
				"TargetCapacitySpecification.TotalTargetCapacity":                      {"2"},
				"TargetCapacitySpecification.DefaultTargetCapacityType":                {"on-demand"},
				"LaunchTemplateConfigs.1.LaunchTemplateSpecification.LaunchTemplateId": {"lt-123"},
				"LaunchTemplateConfigs.1.LaunchTemplateSpecification.Version":          {"$Default"},
				"LaunchTemplateConfigs.1.Overrides.1.SubnetId":                         {"subnet-dc2"},
				"LaunchTemplateConfigs.1.Overrides.1.AvailabilityZone":                 {"us-east-1b"},
				"LaunchTemplateConfigs.1.Overrides.1.Placement.GroupName":              {"pg-spawn"},
				"TagSpecification.1.ResourceType":                                      {"instance"},
				"TagSpecification.1.Tag.1.Key":                                         {"Name"},
				"TagSpecification.1.Tag.1.Value":                                       {"spawned"},
			},
			output: &api.CreateFleetRequest{},
			expected: &api.CreateFleetRequest{
				Type: "instant",
				TargetCapacitySpecification: &api.TargetCapacitySpecificationRequest{
					TotalTargetCapacity:       intPtr(2),
					DefaultTargetCapacityType: func(v string) *string { return &v }("on-demand"),
				},
				LaunchTemplateConfigs: []api.FleetLaunchTemplateConfigRequest{
					{
						LaunchTemplateSpecification: &api.FleetLaunchTemplateSpecificationRequest{
							LaunchTemplateID: func(v string) *string { return &v }("lt-123"),
							Version:          func(v string) *string { return &v }("$Default"),
						},
						Overrides: []api.FleetLaunchTemplateOverridesRequest{
							{
								SubnetID:         func(v string) *string { return &v }("subnet-dc2"),
								AvailabilityZone: func(v string) *string { return &v }("us-east-1b"),
								Placement: &api.FleetPlacementRequest{
									GroupName: func(v string) *string { return &v }("pg-spawn"),
								},
							},
						},
					},
				},
				TagSpecifications: []api.TagSpecification{
					{
						ResourceType: "instance",
						Tags: []api.Tag{
							{Key: "Name", Value: "spawned"},
						},
					},
				},
			},
		},
	}

	// Run tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := decodeURLEncoded(tt.values, tt.output)
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, tt.output)
			}
		})
	}
}
