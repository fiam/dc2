package api

type DescribeSecurityGroupsRequest struct {
	CommonRequest
	DryRunnableRequest
	GroupIDs   []string `url:"GroupId"`
	GroupNames []string `url:"GroupName"`
	Filters    []Filter `url:"Filter"`
}

func (r DescribeSecurityGroupsRequest) Action() Action { return ActionDescribeSecurityGroups }

type CreateSecurityGroupRequest struct {
	CommonRequest
	DryRunnableRequest
	GroupName         string             `url:"GroupName" validate:"required"`
	Description       string             `url:"GroupDescription" validate:"required"`
	VPCID             *string            `url:"VpcId"`
	TagSpecifications []TagSpecification `url:"TagSpecification"`
}

func (r CreateSecurityGroupRequest) Action() Action { return ActionCreateSecurityGroup }

type DeleteSecurityGroupRequest struct {
	CommonRequest
	DryRunnableRequest
	GroupID   *string `url:"GroupId"`
	GroupName *string `url:"GroupName"`
}

func (r DeleteSecurityGroupRequest) Action() Action { return ActionDeleteSecurityGroup }

type SecurityGroupIPPermission struct {
	IPProtocol       *string                    `url:"IpProtocol"`
	FromPort         *int                       `url:"FromPort"`
	ToPort           *int                       `url:"ToPort"`
	IPRanges         []SecurityGroupIPRange     `url:"IpRanges"`
	IPv6Ranges       []SecurityGroupIPv6Range   `url:"Ipv6Ranges"`
	PrefixListIDs    []SecurityGroupPrefixList  `url:"PrefixListIds"`
	UserIDGroupPairs []SecurityGroupUserIDGroup `url:"UserIdGroupPairs"`
}

type SecurityGroupIPRange struct {
	CIDRIP      *string `url:"CidrIp"`
	Description *string `url:"Description"`
}

type SecurityGroupIPv6Range struct {
	CIDRIPv6    *string `url:"CidrIpv6"`
	Description *string `url:"Description"`
}

type SecurityGroupPrefixList struct {
	PrefixListID *string `url:"PrefixListId"`
	Description  *string `url:"Description"`
}

type SecurityGroupUserIDGroup struct {
	Description            *string `url:"Description"`
	GroupID                *string `url:"GroupId"`
	GroupName              *string `url:"GroupName"`
	PeeringStatus          *string `url:"PeeringStatus"`
	UserID                 *string `url:"UserId"`
	VPCID                  *string `url:"VpcId"`
	VPCPeeringConnectionID *string `url:"VpcPeeringConnectionId"`
}

type AuthorizeSecurityGroupIngressRequest struct {
	CommonRequest
	DryRunnableRequest
	GroupID       *string                     `url:"GroupId"`
	GroupName     *string                     `url:"GroupName"`
	CIDRIP        *string                     `url:"CidrIp"`
	IPProtocol    *string                     `url:"IpProtocol"`
	FromPort      *int                        `url:"FromPort"`
	ToPort        *int                        `url:"ToPort"`
	IPPermissions []SecurityGroupIPPermission `url:"IpPermissions"`
}

func (r AuthorizeSecurityGroupIngressRequest) Action() Action {
	return ActionAuthorizeSecurityGroupIngress
}

type AuthorizeSecurityGroupEgressRequest struct {
	CommonRequest
	DryRunnableRequest
	GroupID       *string                     `url:"GroupId"`
	GroupName     *string                     `url:"GroupName"`
	CIDRIP        *string                     `url:"CidrIp"`
	IPProtocol    *string                     `url:"IpProtocol"`
	FromPort      *int                        `url:"FromPort"`
	ToPort        *int                        `url:"ToPort"`
	IPPermissions []SecurityGroupIPPermission `url:"IpPermissions"`
}

func (r AuthorizeSecurityGroupEgressRequest) Action() Action {
	return ActionAuthorizeSecurityGroupEgress
}
