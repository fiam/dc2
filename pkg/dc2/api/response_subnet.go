package api

type DescribeSubnetsResponse struct {
	Subnets   []Subnet `xml:"subnetSet>item"`
	NextToken *string  `xml:"nextToken"`
}

type Subnet struct {
	SubnetID                *string `xml:"subnetId"`
	VPCID                   *string `xml:"vpcId"`
	State                   *string `xml:"state"`
	AvailabilityZone        *string `xml:"availabilityZone"`
	AvailabilityZoneID      *string `xml:"availabilityZoneId"`
	CIDRBlock               *string `xml:"cidrBlock"`
	AvailableIPAddressCount *int    `xml:"availableIpAddressCount"`
	DefaultForAZ            *bool   `xml:"defaultForAz"`
	MapPublicIPOnLaunch     *bool   `xml:"mapPublicIpOnLaunch"`
	Tags                    []Tag   `xml:"tagSet>item"`
}
