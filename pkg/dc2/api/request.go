package api

type Action int

const (
	ActionRunInstances Action = iota + 1
	ActionDescribeInstances
	ActionStopInstances
	ActionStartInstances
	ActionTerminateInstances
)

type Request interface {
	Action() Action
}

type CommonRequest struct {
	Action      string `validate:"required"`
	Version     string
	ClientToken string
}

type RunInstancesRequest struct {
	CommonRequest
	ImageID      string `validate:"required"`
	InstanceType string `validate:"required"`
	KeyName      string
	MinCount     int `validate:"required,gt=0"`
	MaxCount     int `validate:"required,gt=0"`
}

func (r RunInstancesRequest) Action() Action { return ActionRunInstances }

type Filter struct {
	Name   *string
	Values []string
}

type DescribeInstancesRequest struct {
	CommonRequest
	Filters     []Filter
	InstanceIDs []string
}

func (r DescribeInstancesRequest) Action() Action { return ActionDescribeInstances }

type StopInstancesRequest struct {
	CommonRequest
	InstanceIDs []string
	DryRun      bool
	Force       bool
}

func (r StopInstancesRequest) Action() Action { return ActionStopInstances }

type StartInstancesRequest struct {
	CommonRequest
	InstanceIDs []string
	DryRun      bool
}

func (r StartInstancesRequest) Action() Action { return ActionStartInstances }

type TerminateInstancesRequest struct {
	CommonRequest
	InstanceIDs []string
	DryRun      bool
}

func (r TerminateInstancesRequest) Action() Action { return ActionTerminateInstances }
