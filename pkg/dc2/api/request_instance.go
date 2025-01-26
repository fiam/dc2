package api

type RunInstancesRequest struct {
	CommonRequest
	ImageID           string             `url:"ImageId" validate:"required"`
	InstanceType      string             `url:"InstanceType" validate:"required"`
	KeyName           string             `url:"KeyName"`
	MinCount          int                `url:"MinCount" validate:"required,gt=0"`
	MaxCount          int                `url:"MaxCount" validate:"required,gt=0"`
	TagSpecifications []TagSpecification `url:"TagSpecification"`
	Placement         *Placement         `url:"Placement"`
}

func (r RunInstancesRequest) Action() Action { return ActionRunInstances }

type Filter struct {
	Name   *string  `url:"Name"`
	Values []string `url:"Value"`
}

type DescribeInstancesRequest struct {
	CommonRequest
	Filters     []Filter `url:"Filter"`
	InstanceIDs []string `url:"InstanceId"`
}

func (r DescribeInstancesRequest) Action() Action { return ActionDescribeInstances }

type StopInstancesRequest struct {
	CommonRequest
	DryRunnableRequest
	InstanceIDs []string `url:"InstanceId"`
	Force       bool     `url:"Force"`
}

func (r StopInstancesRequest) Action() Action { return ActionStopInstances }

type StartInstancesRequest struct {
	CommonRequest
	InstanceIDs []string `url:"InstanceId"`
	DryRun      bool     `url:"DryRun"`
}

func (r StartInstancesRequest) Action() Action { return ActionStartInstances }

type TerminateInstancesRequest struct {
	CommonRequest
	DryRunnableRequest
	InstanceIDs []string `url:"InstanceId"`
}

func (r TerminateInstancesRequest) Action() Action { return ActionTerminateInstances }
