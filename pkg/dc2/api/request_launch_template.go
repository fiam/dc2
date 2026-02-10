package api

type LaunchTemplateData struct {
	ImageID           string             `url:"ImageId"`
	InstanceType      string             `url:"InstanceType"`
	TagSpecifications []TagSpecification `url:"TagSpecification"`
}

type CreateLaunchTemplateRequest struct {
	CommonRequest
	LaunchTemplateName string             `url:"LaunchTemplateName" validate:"required"`
	LaunchTemplateData LaunchTemplateData `url:"LaunchTemplateData" validate:"required"`
}

func (r CreateLaunchTemplateRequest) Action() Action { return ActionCreateLaunchTemplate }

type DescribeLaunchTemplatesRequest struct {
	CommonRequest
	DryRunnableRequest
	PaginableRequest
	LaunchTemplateIDs   []string `url:"LaunchTemplateId"`
	LaunchTemplateNames []string `url:"LaunchTemplateName"`
}

func (r DescribeLaunchTemplatesRequest) Action() Action { return ActionDescribeLaunchTemplates }

type DeleteLaunchTemplateRequest struct {
	CommonRequest
	DryRunnableRequest
	LaunchTemplateID   *string `url:"LaunchTemplateId"`
	LaunchTemplateName *string `url:"LaunchTemplateName"`
}

func (r DeleteLaunchTemplateRequest) Action() Action { return ActionDeleteLaunchTemplate }

type CreateLaunchTemplateVersionRequest struct {
	CommonRequest
	DryRunnableRequest
	LaunchTemplateID   *string            `url:"LaunchTemplateId"`
	LaunchTemplateName *string            `url:"LaunchTemplateName"`
	SourceVersion      *string            `url:"SourceVersion"`
	VersionDescription *string            `url:"VersionDescription"`
	LaunchTemplateData LaunchTemplateData `url:"LaunchTemplateData" validate:"required"`
}

func (r CreateLaunchTemplateVersionRequest) Action() Action { return ActionCreateLaunchTemplateVersion }

type DescribeLaunchTemplateVersionsRequest struct {
	CommonRequest
	DryRunnableRequest
	PaginableRequest
	LaunchTemplateID   *string  `url:"LaunchTemplateId"`
	LaunchTemplateName *string  `url:"LaunchTemplateName"`
	MinVersion         *string  `url:"MinVersion"`
	MaxVersion         *string  `url:"MaxVersion"`
	Versions           []string `url:"LaunchTemplateVersion"`
}

func (r DescribeLaunchTemplateVersionsRequest) Action() Action {
	return ActionDescribeLaunchTemplateVersions
}

type ModifyLaunchTemplateRequest struct {
	CommonRequest
	DryRunnableRequest
	LaunchTemplateID   *string `url:"LaunchTemplateId"`
	LaunchTemplateName *string `url:"LaunchTemplateName"`
	SetDefaultVersion  *string `url:"SetDefaultVersion"`
}

func (r ModifyLaunchTemplateRequest) Action() Action { return ActionModifyLaunchTemplate }
