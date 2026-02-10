package api

import "time"

type CreateAutoScalingGroupResponse struct{}

type UpdateAutoScalingGroupResponse struct{}

type SetDesiredCapacityResponse struct{}

type DeleteAutoScalingGroupResponse struct{}

type DescribeAutoScalingGroupsResponse struct {
	DescribeAutoScalingGroupsResult DescribeAutoScalingGroupsResult `xml:"DescribeAutoScalingGroupsResult"`
}

type DescribeAutoScalingGroupsResult struct {
	AutoScalingGroups []AutoScalingGroup `xml:"AutoScalingGroups>member"`
	NextToken         *string            `xml:"NextToken"`
}

type AutoScalingGroup struct {
	AutoScalingGroupName *string                                 `xml:"AutoScalingGroupName"`
	CreatedTime          *time.Time                              `xml:"CreatedTime"`
	DefaultCooldown      *int                                    `xml:"DefaultCooldown"`
	DesiredCapacity      *int                                    `xml:"DesiredCapacity"`
	HealthCheckType      *string                                 `xml:"HealthCheckType"`
	Instances            []AutoScalingInstance                   `xml:"Instances>member"`
	LaunchTemplate       *AutoScalingLaunchTemplateSpecification `xml:"LaunchTemplate"`
	MaxSize              *int                                    `xml:"MaxSize"`
	MinSize              *int                                    `xml:"MinSize"`
	VPCZoneIdentifier    *string                                 `xml:"VPCZoneIdentifier"`
	AvailabilityZones    []string                                `xml:"AvailabilityZones>member"`
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
