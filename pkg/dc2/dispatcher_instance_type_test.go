package dc2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/instancetype"
)

func TestDispatchDescribeInstanceTypes(t *testing.T) {
	t.Parallel()

	d := &Dispatcher{
		opts: DispatcherOptions{Region: "us-east-1"},
		instanceTypeCatalog: &instancetype.Catalog{
			InstanceTypes: map[string]map[string]any{
				"m7g.large": {
					"InstanceType": "m7g.large",
				},
				"c7g.large": {
					"InstanceType": "c7g.large",
				},
			},
		},
	}
	filterName := "instance-type"

	resp, err := d.dispatchDescribeInstanceTypes(&api.DescribeInstanceTypesRequest{
		Filters: []api.Filter{{Name: &filterName, Values: []string{"c7g.large"}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.InstanceTypes, 1)
	assert.Equal(t, "c7g.large", resp.InstanceTypes[0]["InstanceType"])
}

func TestDispatchDescribeInstanceTypeOfferingsTreatsTypesAsGlobal(t *testing.T) {
	t.Parallel()

	d := &Dispatcher{
		opts: DispatcherOptions{Region: "us-east-1"},
		instanceTypeCatalog: &instancetype.Catalog{
			InstanceTypes: map[string]map[string]any{
				"m7g.large": {"InstanceType": "m7g.large"},
				"c7g.large": {"InstanceType": "c7g.large"},
			},
		},
	}
	locationFilterName := "location"

	resp, err := d.dispatchDescribeInstanceTypeOfferings(&api.DescribeInstanceTypeOfferingsRequest{
		LocationType: "region",
		Filters:      []api.Filter{{Name: &locationFilterName, Values: []string{"eu-west-1"}}},
	})
	require.NoError(t, err)
	require.Len(t, resp.InstanceTypeOfferings, 2)
	for _, offering := range resp.InstanceTypeOfferings {
		assert.Equal(t, "region", offering.LocationType)
		assert.Equal(t, "eu-west-1", offering.Location)
	}
	assert.Equal(t, "c7g.large", resp.InstanceTypeOfferings[0].InstanceType)
	assert.Equal(t, "m7g.large", resp.InstanceTypeOfferings[1].InstanceType)
}

func TestDispatchGetInstanceTypesFromRequirements(t *testing.T) {
	t.Parallel()

	d := &Dispatcher{
		opts: DispatcherOptions{Region: "us-east-1"},
		instanceTypeCatalog: &instancetype.Catalog{
			InstanceTypes: map[string]map[string]any{
				"c7g.large": {
					"InstanceType":                 "c7g.large",
					"CurrentGeneration":            true,
					"SupportedVirtualizationTypes": []any{"hvm"},
					"ProcessorInfo": map[string]any{
						"Manufacturer":           "amazon-web-services",
						"SupportedArchitectures": []any{"arm64"},
					},
					"VCpuInfo":   map[string]any{"DefaultVCpus": int64(2)},
					"MemoryInfo": map[string]any{"SizeInMiB": int64(4096)},
				},
				"m5.large": {
					"InstanceType":                 "m5.large",
					"CurrentGeneration":            true,
					"SupportedVirtualizationTypes": []any{"hvm"},
					"ProcessorInfo": map[string]any{
						"Manufacturer":           "intel",
						"SupportedArchitectures": []any{"x86_64"},
					},
					"VCpuInfo":   map[string]any{"DefaultVCpus": int64(2)},
					"MemoryInfo": map[string]any{"SizeInMiB": int64(8192)},
				},
			},
		},
	}
	vcpuMin := 2
	vcpuMax := 2
	memoryMin := 4096
	memoryMax := 4096

	resp, err := d.dispatchGetInstanceTypesFromInstanceRequirements(&api.GetInstanceTypesFromInstanceRequirementsRequest{
		ArchitectureTypes:   []string{"arm64"},
		VirtualizationTypes: []string{"hvm"},
		InstanceRequirements: &api.InstanceRequirementsRequest{
			VCPUCount: &api.IntRangeRequest{Min: &vcpuMin, Max: &vcpuMax},
			MemoryMiB: &api.IntRangeRequest{Min: &memoryMin, Max: &memoryMax},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.InstanceTypes, 1)
	assert.Equal(t, "c7g.large", resp.InstanceTypes[0].InstanceType)
}
