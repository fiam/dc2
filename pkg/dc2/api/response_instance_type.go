package api

type DescribeInstanceTypesResponse struct {
	InstanceTypes []map[string]any `xml:"instanceTypeSet>item"`
	NextToken     *string          `xml:"nextToken"`
}

type InstanceTypeOffering struct {
	InstanceType string `xml:"instanceType"`
	LocationType string `xml:"locationType"`
	Location     string `xml:"location"`
}

type DescribeInstanceTypeOfferingsResponse struct {
	InstanceTypeOfferings []InstanceTypeOffering `xml:"instanceTypeOfferings>item"`
	NextToken             *string                `xml:"nextToken"`
}

type InstanceTypeInfoFromInstanceRequirements struct {
	InstanceType string `xml:"instanceType"`
}

type GetInstanceTypesFromInstanceRequirementsResponse struct {
	InstanceTypes []InstanceTypeInfoFromInstanceRequirements `xml:"instanceTypesSet>item"`
	NextToken     *string                                    `xml:"nextToken"`
}
