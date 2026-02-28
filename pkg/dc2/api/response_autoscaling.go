package api

import "time"

type CreateAutoScalingGroupResponse struct{}

type CreateOrUpdateTagsResponse struct{}

type UpdateAutoScalingGroupResponse struct{}

type SetDesiredCapacityResponse struct{}

type DetachInstancesResponse struct {
	DetachInstancesResult DetachInstancesResult `xml:"DetachInstancesResult"`
}

type DetachInstancesResult struct{}

type DeleteAutoScalingGroupResponse struct{}

type PutWarmPoolResponse struct {
	PutWarmPoolResult PutWarmPoolResult `xml:"PutWarmPoolResult"`
}

type PutWarmPoolResult struct{}

type DeleteWarmPoolResponse struct {
	DeleteWarmPoolResult DeleteWarmPoolResult `xml:"DeleteWarmPoolResult"`
}

type DeleteWarmPoolResult struct{}

type DescribeAutoScalingGroupsResponse struct {
	DescribeAutoScalingGroupsResult DescribeAutoScalingGroupsResult `xml:"DescribeAutoScalingGroupsResult"`
}

type DescribeWarmPoolResponse struct {
	DescribeWarmPoolResult DescribeWarmPoolResult `xml:"DescribeWarmPoolResult"`
}

type DescribeAutoScalingGroupsResult struct {
	AutoScalingGroups []AutoScalingGroup `xml:"AutoScalingGroups>member"`
	NextToken         *string            `xml:"NextToken"`
}

type DescribeWarmPoolResult struct {
	Instances             []AutoScalingInstance  `xml:"Instances>member"`
	NextToken             *string                `xml:"NextToken"`
	WarmPoolConfiguration *WarmPoolConfiguration `xml:"WarmPoolConfiguration"`
}

type AutoScalingGroup struct {
	AutoScalingGroupName  *string                                 `xml:"AutoScalingGroupName"`
	CreatedTime           *time.Time                              `xml:"CreatedTime"`
	DefaultCooldown       *int                                    `xml:"DefaultCooldown"`
	DesiredCapacity       *int                                    `xml:"DesiredCapacity"`
	HealthCheckType       *string                                 `xml:"HealthCheckType"`
	Instances             []AutoScalingInstance                   `xml:"Instances>member"`
	LaunchTemplate        *AutoScalingLaunchTemplateSpecification `xml:"LaunchTemplate"`
	MaxSize               *int                                    `xml:"MaxSize"`
	MinSize               *int                                    `xml:"MinSize"`
	Tags                  []AutoScalingTagDescription             `xml:"Tags>member"`
	VPCZoneIdentifier     *string                                 `xml:"VPCZoneIdentifier"`
	AvailabilityZones     []string                                `xml:"AvailabilityZones>member"`
	WarmPoolConfiguration *WarmPoolConfiguration                  `xml:"WarmPoolConfiguration"`
	WarmPoolSize          *int                                    `xml:"WarmPoolSize"`
}

type AutoScalingTagDescription struct {
	Key               *string `xml:"Key"`
	Value             *string `xml:"Value"`
	PropagateAtLaunch *bool   `xml:"PropagateAtLaunch"`
	ResourceID        *string `xml:"ResourceId"`
	ResourceType      *string `xml:"ResourceType"`
}

type WarmPoolConfiguration struct {
	InstanceReusePolicy      *WarmPoolInstanceReusePolicy `xml:"InstanceReusePolicy"`
	MaxGroupPreparedCapacity *int                         `xml:"MaxGroupPreparedCapacity"`
	MinSize                  *int                         `xml:"MinSize"`
	PoolState                *string                      `xml:"PoolState"`
	Status                   *string                      `xml:"Status"`
}

type AutoScalingInstance struct {
	AvailabilityZone     *string                                 `xml:"AvailabilityZone"`
	HealthStatus         *string                                 `xml:"HealthStatus"`
	InstanceID           *string                                 `xml:"InstanceId"`
	InstanceType         *string                                 `xml:"InstanceType"`
	LaunchTemplate       *AutoScalingLaunchTemplateSpecification `xml:"LaunchTemplate"`
	LifecycleState       string                                  `xml:"LifecycleState"`
	ProtectedFromScaleIn *bool                                   `xml:"ProtectedFromScaleIn"`
}
