package api

type DescribeSecurityGroupsResponse struct {
	SecurityGroups []SecurityGroup `xml:"securityGroupInfo>item"`
}

type CreateSecurityGroupResponse struct {
	GroupID *string `xml:"groupId"`
}

type DeleteSecurityGroupResponse struct{}

type SecurityGroupRuleMutationResponse struct {
	Return bool `xml:"return"`
}

type SecurityGroup struct {
	GroupID          *string `xml:"groupId"`
	GroupName        *string `xml:"groupName"`
	GroupDescription *string `xml:"groupDescription"`
	OwnerID          *string `xml:"ownerId"`
	VPCID            *string `xml:"vpcId"`
	Tags             []Tag   `xml:"tagSet>item"`
}
