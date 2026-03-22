package api

type AutoScalingLaunchTemplateSpecification struct {
	LaunchTemplateID   *string `url:"LaunchTemplateId" xml:"LaunchTemplateId"`
	LaunchTemplateName *string `url:"LaunchTemplateName" xml:"LaunchTemplateName"`
	Version            *string `url:"Version" xml:"Version"`
}

type AutoScalingMixedInstancesInstancesDistribution struct {
	OnDemandAllocationStrategy          *string `url:"OnDemandAllocationStrategy" xml:"OnDemandAllocationStrategy"`
	OnDemandBaseCapacity                *int    `url:"OnDemandBaseCapacity" xml:"OnDemandBaseCapacity"`
	OnDemandPercentageAboveBaseCapacity *int    `url:"OnDemandPercentageAboveBaseCapacity" xml:"OnDemandPercentageAboveBaseCapacity"`
	SpotAllocationStrategy              *string `url:"SpotAllocationStrategy" xml:"SpotAllocationStrategy"`
	SpotInstancePools                   *int    `url:"SpotInstancePools" xml:"SpotInstancePools"`
	SpotMaxPrice                        *string `url:"SpotMaxPrice" xml:"SpotMaxPrice"`
}

type AutoScalingMixedInstancesLaunchTemplateOverrides struct {
	ImageID                     *string                                 `url:"ImageId" xml:"ImageId"`
	InstanceRequirements        *InstanceRequirementsRequest            `url:"InstanceRequirements" xml:"InstanceRequirements"`
	InstanceType                *string                                 `url:"InstanceType" xml:"InstanceType"`
	LaunchTemplateSpecification *AutoScalingLaunchTemplateSpecification `url:"LaunchTemplateSpecification" xml:"LaunchTemplateSpecification"`
	WeightedCapacity            *string                                 `url:"WeightedCapacity" xml:"WeightedCapacity"`
}

type AutoScalingMixedInstancesLaunchTemplate struct {
	LaunchTemplateSpecification *AutoScalingLaunchTemplateSpecification            `url:"LaunchTemplateSpecification" xml:"LaunchTemplateSpecification"`
	Overrides                   []AutoScalingMixedInstancesLaunchTemplateOverrides `url:"Overrides" xml:"Overrides>member"`
}

type AutoScalingMixedInstancesPolicy struct {
	InstancesDistribution *AutoScalingMixedInstancesInstancesDistribution `url:"InstancesDistribution" xml:"InstancesDistribution"`
	LaunchTemplate        *AutoScalingMixedInstancesLaunchTemplate        `url:"LaunchTemplate" xml:"LaunchTemplate"`
}

type CreateAutoScalingGroupRequest struct {
	CommonRequest
	AutoScalingGroupName string                                  `url:"AutoScalingGroupName" validate:"required"`
	MinSize              *int                                    `url:"MinSize" validate:"required,gte=0"`
	MaxSize              *int                                    `url:"MaxSize" validate:"required,gte=0"`
	DesiredCapacity      *int                                    `url:"DesiredCapacity"`
	LaunchTemplate       *AutoScalingLaunchTemplateSpecification `url:"LaunchTemplate"`
	MixedInstancesPolicy *AutoScalingMixedInstancesPolicy        `url:"MixedInstancesPolicy"`
	Tags                 []AutoScalingTag                        `url:"Tags"`
	AvailabilityZones    []string                                `url:"AvailabilityZones"`
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

type LaunchInstancesRequest struct {
	CommonRequest
	AutoScalingGroupName string   `url:"AutoScalingGroupName" validate:"required"`
	AvailabilityZoneIDs  []string `url:"AvailabilityZoneIds"`
	AvailabilityZones    []string `url:"AvailabilityZones"`
	RequestedCapacity    *int     `url:"RequestedCapacity" validate:"required,gt=0"`
	RetryStrategy        *string  `url:"RetryStrategy"`
	SubnetIDs            []string `url:"SubnetIds"`
}

func (r LaunchInstancesRequest) Action() Action { return ActionLaunchInstances }

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
	MixedInstancesPolicy *AutoScalingMixedInstancesPolicy        `url:"MixedInstancesPolicy"`
	AvailabilityZones    []string                                `url:"AvailabilityZones"`
	VPCZoneIdentifier    *string                                 `url:"VPCZoneIdentifier"`
}

func (r UpdateAutoScalingGroupRequest) Action() Action { return ActionUpdateAutoScalingGroup }

type SetDesiredCapacityRequest struct {
	CommonRequest
	AutoScalingGroupName string `url:"AutoScalingGroupName" validate:"required"`
	DesiredCapacity      *int   `url:"DesiredCapacity" validate:"required,gte=0"`
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

type WarmPoolInstanceReusePolicy struct {
	ReuseOnScaleIn *bool `url:"ReuseOnScaleIn" xml:"ReuseOnScaleIn"`
}

type PutWarmPoolRequest struct {
	CommonRequest
	AutoScalingGroupName     string                       `url:"AutoScalingGroupName" validate:"required"`
	MaxGroupPreparedCapacity *int                         `url:"MaxGroupPreparedCapacity"`
	MinSize                  *int                         `url:"MinSize"`
	PoolState                *string                      `url:"PoolState"`
	InstanceReusePolicy      *WarmPoolInstanceReusePolicy `url:"InstanceReusePolicy"`
}

func (r PutWarmPoolRequest) Action() Action { return ActionPutWarmPool }

type DescribeWarmPoolRequest struct {
	CommonRequest
	AutoScalingGroupName string  `url:"AutoScalingGroupName" validate:"required"`
	MaxRecords           *int    `url:"MaxRecords"`
	NextToken            *string `url:"NextToken"`
}

func (r DescribeWarmPoolRequest) Action() Action { return ActionDescribeWarmPool }

type DeleteWarmPoolRequest struct {
	CommonRequest
	AutoScalingGroupName string `url:"AutoScalingGroupName" validate:"required"`
	ForceDelete          *bool  `url:"ForceDelete"`
}

func (r DeleteWarmPoolRequest) Action() Action { return ActionDeleteWarmPool }

type AutoScalingTag struct {
	Key               *string `url:"Key"`
	Value             *string `url:"Value"`
	ResourceID        *string `url:"ResourceId"`
	ResourceType      *string `url:"ResourceType"`
	PropagateAtLaunch *bool   `url:"PropagateAtLaunch"`
}

type CreateOrUpdateAutoScalingTagsRequest struct {
	CommonRequest
	Tags []AutoScalingTag `url:"Tags" validate:"required,min=1,dive"`
}

func (r CreateOrUpdateAutoScalingTagsRequest) Action() Action {
	return ActionCreateOrUpdateAutoScalingTags
}
