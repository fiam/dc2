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
	Min *int `url:"Min" xml:"min"`
	Max *int `url:"Max" xml:"max"`
}

type FloatRangeRequest struct {
	Min *float64 `url:"Min" xml:"min"`
	Max *float64 `url:"Max" xml:"max"`
}

type BaselinePerformanceFactorsRequest struct {
	CPU *CPUPerformanceFactorRequest `url:"Cpu" xml:"cpu"`
}

type CPUPerformanceFactorRequest struct {
	References []PerformanceFactorReferenceRequest `url:"Reference" xml:"referenceSet>item"`
}

type PerformanceFactorReferenceRequest struct {
	InstanceFamily *string `url:"InstanceFamily" xml:"instanceFamily"`
}

type InstanceRequirementsRequest struct {
	MemoryMiB *IntRangeRequest `url:"MemoryMiB" validate:"required" xml:"memoryMiB"`
	VCPUCount *IntRangeRequest `url:"VCpuCount" validate:"required" xml:"vCpuCount"`

	AcceleratorCount                               *IntRangeRequest                   `url:"AcceleratorCount" xml:"acceleratorCount"`
	AcceleratorManufacturers                       []string                           `url:"AcceleratorManufacturer" xml:"acceleratorManufacturerSet>item"`
	AcceleratorNames                               []string                           `url:"AcceleratorName" xml:"acceleratorNameSet>item"`
	AcceleratorTotalMemoryMiB                      *IntRangeRequest                   `url:"AcceleratorTotalMemoryMiB" xml:"acceleratorTotalMemoryMiB"`
	AcceleratorTypes                               []string                           `url:"AcceleratorType" xml:"acceleratorTypeSet>item"`
	AllowedInstanceTypes                           []string                           `url:"AllowedInstanceType" xml:"allowedInstanceTypeSet>item"`
	BareMetal                                      *string                            `url:"BareMetal" xml:"bareMetal"`
	BaselineEbsBandwidthMbps                       *IntRangeRequest                   `url:"BaselineEbsBandwidthMbps" xml:"baselineEbsBandwidthMbps"`
	BaselinePerformanceFactors                     *BaselinePerformanceFactorsRequest `url:"BaselinePerformanceFactors" xml:"baselinePerformanceFactors"`
	BurstablePerformance                           *string                            `url:"BurstablePerformance" xml:"burstablePerformance"`
	CPUManufacturers                               []string                           `url:"CpuManufacturer" xml:"cpuManufacturerSet>item"`
	ExcludedInstanceTypes                          []string                           `url:"ExcludedInstanceType" xml:"excludedInstanceTypeSet>item"`
	InstanceGenerations                            []string                           `url:"InstanceGeneration" xml:"instanceGenerationSet>item"`
	LocalStorage                                   *string                            `url:"LocalStorage" xml:"localStorage"`
	LocalStorageTypes                              []string                           `url:"LocalStorageType" xml:"localStorageTypeSet>item"`
	MaxSpotPriceAsPercentageOfOptimalOnDemandPrice *int                               `url:"MaxSpotPriceAsPercentageOfOptimalOnDemandPrice" xml:"maxSpotPriceAsPercentageOfOptimalOnDemandPrice"`
	MemoryGiBPerVCPU                               *FloatRangeRequest                 `url:"MemoryGiBPerVCpu" xml:"memoryGiBPerVCpu"`
	NetworkBandwidthGbps                           *FloatRangeRequest                 `url:"NetworkBandwidthGbps" xml:"networkBandwidthGbps"`
	NetworkInterfaceCount                          *IntRangeRequest                   `url:"NetworkInterfaceCount" xml:"networkInterfaceCount"`
	OnDemandMaxPricePercentageOverLowestPrice      *int                               `url:"OnDemandMaxPricePercentageOverLowestPrice" xml:"onDemandMaxPricePercentageOverLowestPrice"`
	RequireHibernateSupport                        *bool                              `url:"RequireHibernateSupport" xml:"requireHibernateSupport"`
	SpotMaxPricePercentageOverLowestPrice          *int                               `url:"SpotMaxPricePercentageOverLowestPrice" xml:"spotMaxPricePercentageOverLowestPrice"`
	TotalLocalStorageGB                            *FloatRangeRequest                 `url:"TotalLocalStorageGB" xml:"totalLocalStorageGB"`
}
