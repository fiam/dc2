package api

type DescribeSecurityGroupsRequest struct {
	CommonRequest
	DryRunnableRequest
	GroupIDs   []string `url:"GroupId"`
	GroupNames []string `url:"GroupName"`
	Filters    []Filter `url:"Filter"`
}

func (r DescribeSecurityGroupsRequest) Action() Action { return ActionDescribeSecurityGroups }
