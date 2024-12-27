package api

import "github.com/fiam/dc2/pkg/dc2/types"

type Action int

const (
	ActionRunInstances Action = iota + 1
	ActionDescribeInstances
	ActionStopInstances
	ActionStartInstances
	ActionTerminateInstances
	ActionCreateTags
	ActionDeleteTags
)

type Request interface {
	Action() Action
}

type CommonRequest struct {
	Action      string `url:"Action" validate:"required"`
	Version     string `url:"Version"`
	ClientToken string `url:"ClientToken"`
}

type RunInstancesRequest struct {
	CommonRequest
	ImageID           string             `url:"ImageId" validate:"required"`
	InstanceType      string             `url:"InstanceType" validate:"required"`
	KeyName           string             `url:"KeyName"`
	MinCount          int                `url:"MinCount" validate:"required,gt=0"`
	MaxCount          int                `url:"MaxCount" validate:"required,gt=0"`
	TagSpecifications []TagSpecification `url:"TagSpecification"`
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
	InstanceIDs []string `url:"InstanceId"`
	DryRun      bool     `url:"DryRun"`
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
	InstanceIDs []string `url:"InstanceId"`
	DryRun      bool     `url:"DryRun"`
}

func (r TerminateInstancesRequest) Action() Action { return ActionTerminateInstances }

type CreateTagsRequest struct {
	CommonRequest
	ResourceIDs []string `url:"ResourceId" validate:"required"`
	Tags        []Tag    `url:"Tag" validate:"required"`
	DryRun      bool     `url:"DryRun"`
}

func (r CreateTagsRequest) Action() Action { return ActionCreateTags }

type DeleteTagsRequest struct {
	CommonRequest
	ResourceIDs []string `url:"ResourceId" validate:"required"`
	Tags        []Tag    `url:"Tag" validate:"required"`
	DryRun      bool     `url:"DryRun"`
}

func (r DeleteTagsRequest) Action() Action { return ActionDeleteTags }

type TagSpecification struct {
	ResourceType types.ResourceType `url:"ResourceType" validate:"required"`
	Tags         []Tag              `url:"Tag"`
}
