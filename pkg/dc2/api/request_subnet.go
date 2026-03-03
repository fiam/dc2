package api

type DescribeSubnetsRequest struct {
	CommonRequest
	DryRunnableRequest
	SubnetIDs []string `url:"SubnetId"`
	Filters   []Filter `url:"Filter"`
	PaginableRequest
}

func (r DescribeSubnetsRequest) Action() Action { return ActionDescribeSubnets }
