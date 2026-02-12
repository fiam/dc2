package api

type AutoScalingLaunchTemplateSpecification struct {
	LaunchTemplateID   *string `url:"LaunchTemplateId" xml:"LaunchTemplateId"`
	LaunchTemplateName *string `url:"LaunchTemplateName" xml:"LaunchTemplateName"`
	Version            *string `url:"Version" xml:"Version"`
}

type CreateAutoScalingGroupRequest struct {
	CommonRequest
	AutoScalingGroupName string                                  `url:"AutoScalingGroupName" validate:"required"`
	MinSize              int                                     `url:"MinSize" validate:"required,gte=0"`
	MaxSize              int                                     `url:"MaxSize" validate:"required,gte=0"`
	DesiredCapacity      *int                                    `url:"DesiredCapacity"`
	LaunchTemplate       *AutoScalingLaunchTemplateSpecification `url:"LaunchTemplate"`
	VPCZoneIdentifier    *string                                 `url:"VPCZoneIdentifier"`
}

func (r CreateAutoScalingGroupRequest) Action() Action { return ActionCreateAutoScalingGroup }

type DescribeAutoScalingGroupsRequest struct {
	CommonRequest
	AutoScalingGroupNames []string            `url:"AutoScalingGroupNames"`
	Filters               []AutoScalingFilter `url:"Filters"`
	MaxRecords            *int                `url:"MaxRecords"`
	NextToken             *string             `url:"NextToken"`
	IncludeInstances      *bool               `url:"IncludeInstances"`
}

func (r DescribeAutoScalingGroupsRequest) Action() Action { return ActionDescribeAutoScalingGroups }

type AutoScalingFilter struct {
	Name   *string  `url:"Name"`
	Values []string `url:"Values"`
}

type UpdateAutoScalingGroupRequest struct {
	CommonRequest
	AutoScalingGroupName string                                  `url:"AutoScalingGroupName" validate:"required"`
	MinSize              *int                                    `url:"MinSize"`
	MaxSize              *int                                    `url:"MaxSize"`
	DesiredCapacity      *int                                    `url:"DesiredCapacity"`
	LaunchTemplate       *AutoScalingLaunchTemplateSpecification `url:"LaunchTemplate"`
	VPCZoneIdentifier    *string                                 `url:"VPCZoneIdentifier"`
}

func (r UpdateAutoScalingGroupRequest) Action() Action { return ActionUpdateAutoScalingGroup }

type SetDesiredCapacityRequest struct {
	CommonRequest
	AutoScalingGroupName string `url:"AutoScalingGroupName" validate:"required"`
	DesiredCapacity      int    `url:"DesiredCapacity" validate:"required,gte=0"`
	HonorCooldown        *bool  `url:"HonorCooldown"`
}

func (r SetDesiredCapacityRequest) Action() Action { return ActionSetDesiredCapacity }

type DetachInstancesRequest struct {
	CommonRequest
	AutoScalingGroupName           string   `url:"AutoScalingGroupName" validate:"required"`
	InstanceIDs                    []string `url:"InstanceIds" validate:"required,min=1,dive,required"`
	ShouldDecrementDesiredCapacity *bool    `url:"ShouldDecrementDesiredCapacity" validate:"required"`
}

func (r DetachInstancesRequest) Action() Action { return ActionDetachInstances }

type DeleteAutoScalingGroupRequest struct {
	CommonRequest
	AutoScalingGroupName string `url:"AutoScalingGroupName" validate:"required"`
	ForceDelete          *bool  `url:"ForceDelete"`
}

func (r DeleteAutoScalingGroupRequest) Action() Action { return ActionDeleteAutoScalingGroup }
