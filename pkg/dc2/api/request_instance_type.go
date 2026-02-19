package api

type DescribeInstanceTypesRequest struct {
	CommonRequest
	DryRunnableRequest
	Filters       []Filter `url:"Filter"`
	InstanceTypes []string `url:"InstanceType"`
	PaginableRequest
}

func (r DescribeInstanceTypesRequest) Action() Action { return ActionDescribeInstanceTypes }

type DescribeInstanceTypeOfferingsRequest struct {
	CommonRequest
	DryRunnableRequest
	Filters      []Filter `url:"Filter"`
	LocationType string   `url:"LocationType"`
	PaginableRequest
}

func (r DescribeInstanceTypeOfferingsRequest) Action() Action {
	return ActionDescribeInstanceTypeOfferings
}

type GetInstanceTypesFromInstanceRequirementsRequest struct {
	CommonRequest
	DryRunnableRequest
	ArchitectureTypes    []string                     `url:"ArchitectureType" validate:"required"`
	InstanceRequirements *InstanceRequirementsRequest `url:"InstanceRequirements" validate:"required"`
	VirtualizationTypes  []string                     `url:"VirtualizationType" validate:"required"`
	PaginableRequest
}

func (r GetInstanceTypesFromInstanceRequirementsRequest) Action() Action {
	return ActionGetInstanceTypesFromInstanceRequirements
}

type IntRangeRequest struct {
	Min *int `url:"Min"`
	Max *int `url:"Max"`
}

type FloatRangeRequest struct {
	Min *float64 `url:"Min"`
	Max *float64 `url:"Max"`
}

type BaselinePerformanceFactorsRequest struct {
	CPU *CPUPerformanceFactorRequest `url:"Cpu"`
}

type CPUPerformanceFactorRequest struct {
	References []PerformanceFactorReferenceRequest `url:"References"`
}

type PerformanceFactorReferenceRequest struct {
	InstanceFamily *string `url:"InstanceFamily"`
}

type InstanceRequirementsRequest struct {
	MemoryMiB *IntRangeRequest `url:"MemoryMiB" validate:"required"`
	VCPUCount *IntRangeRequest `url:"VCpuCount" validate:"required"`

	AcceleratorCount                               *IntRangeRequest                   `url:"AcceleratorCount"`
	AcceleratorManufacturers                       []string                           `url:"AcceleratorManufacturer"`
	AcceleratorNames                               []string                           `url:"AcceleratorName"`
	AcceleratorTotalMemoryMiB                      *IntRangeRequest                   `url:"AcceleratorTotalMemoryMiB"`
	AcceleratorTypes                               []string                           `url:"AcceleratorType"`
	AllowedInstanceTypes                           []string                           `url:"AllowedInstanceType"`
	BareMetal                                      *string                            `url:"BareMetal"`
	BaselineEbsBandwidthMbps                       *IntRangeRequest                   `url:"BaselineEbsBandwidthMbps"`
	BaselinePerformanceFactors                     *BaselinePerformanceFactorsRequest `url:"BaselinePerformanceFactors"`
	BurstablePerformance                           *string                            `url:"BurstablePerformance"`
	CPUManufacturers                               []string                           `url:"CpuManufacturer"`
	ExcludedInstanceTypes                          []string                           `url:"ExcludedInstanceType"`
	InstanceGenerations                            []string                           `url:"InstanceGeneration"`
	LocalStorage                                   *string                            `url:"LocalStorage"`
	LocalStorageTypes                              []string                           `url:"LocalStorageType"`
	MaxSpotPriceAsPercentageOfOptimalOnDemandPrice *int                               `url:"MaxSpotPriceAsPercentageOfOptimalOnDemandPrice"`
	MemoryGiBPerVCPU                               *FloatRangeRequest                 `url:"MemoryGiBPerVCpu"`
	NetworkBandwidthGbps                           *FloatRangeRequest                 `url:"NetworkBandwidthGbps"`
	NetworkInterfaceCount                          *IntRangeRequest                   `url:"NetworkInterfaceCount"`
	OnDemandMaxPricePercentageOverLowestPrice      *int                               `url:"OnDemandMaxPricePercentageOverLowestPrice"`
	RequireHibernateSupport                        *bool                              `url:"RequireHibernateSupport"`
	SpotMaxPricePercentageOverLowestPrice          *int                               `url:"SpotMaxPricePercentageOverLowestPrice"`
	TotalLocalStorageGB                            *FloatRangeRequest                 `url:"TotalLocalStorageGB"`
}
