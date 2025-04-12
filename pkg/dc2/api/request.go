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
	ActionCreateVolume
	ActionDeleteVolume
	ActionAttachVolume
	ActionDetachVolume
	ActionDescribeVolumes
)

type Request interface {
	Action() Action
}

type CommonRequest struct {
	Action      string `url:"Action" validate:"required"`
	Version     string `url:"Version"`
	ClientToken string `url:"ClientToken"`
}

type DryRunnableRequest struct {
	DryRun bool `url:"DryRun"`
}

type PaginableRequest struct {
	NextToken  *string `url:"NextToken"`
	MaxResults *int    `url:"MaxResults"`
}

type CreateTagsRequest struct {
	CommonRequest
	DryRunnableRequest
	ResourceIDs []string `url:"ResourceId" validate:"required"`
	Tags        []Tag    `url:"Tag" validate:"required"`
}

func (r CreateTagsRequest) Action() Action { return ActionCreateTags }

type DeleteTagsRequest struct {
	CommonRequest
	DryRunnableRequest
	ResourceIDs []string `url:"ResourceId" validate:"required"`
	Tags        []Tag    `url:"Tag" validate:"required"`
}

func (r DeleteTagsRequest) Action() Action { return ActionDeleteTags }

type TagSpecification struct {
	ResourceType types.ResourceType `url:"ResourceType" validate:"required"`
	Tags         []Tag              `url:"Tag"`
}
