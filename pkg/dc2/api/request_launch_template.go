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
