package api

type DescribeSecurityGroupsResponse struct {
	SecurityGroups []SecurityGroup `xml:"securityGroupInfo>item"`
}

type SecurityGroup struct {
	GroupID          *string `xml:"groupId"`
	GroupName        *string `xml:"groupName"`
	GroupDescription *string `xml:"groupDescription"`
	OwnerID          *string `xml:"ownerId"`
	VPCID            *string `xml:"vpcId"`
	Tags             []Tag   `xml:"tagSet>item"`
}
