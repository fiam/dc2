package api

type FleetLaunchTemplateSpecificationRequest struct {
	LaunchTemplateID   *string `url:"LaunchTemplateId"`
	LaunchTemplateName *string `url:"LaunchTemplateName"`
	Version            *string `url:"Version"`
}

type FleetPlacementRequest struct {
	GroupName *string `url:"GroupName"`
}

type FleetLaunchTemplateOverridesRequest struct {
	AvailabilityZone     *string                      `url:"AvailabilityZone"`
	ImageID              *string                      `url:"ImageId"`
	InstanceRequirements *InstanceRequirementsRequest `url:"InstanceRequirements"`
	InstanceType         *string                      `url:"InstanceType"`
	Placement            *FleetPlacementRequest       `url:"Placement"`
	SubnetID             *string                      `url:"SubnetId"`
}

type FleetLaunchTemplateConfigRequest struct {
	LaunchTemplateSpecification *FleetLaunchTemplateSpecificationRequest `url:"LaunchTemplateSpecification" validate:"required"`
	Overrides                   []FleetLaunchTemplateOverridesRequest    `url:"Overrides"`
}

type TargetCapacitySpecificationRequest struct {
	DefaultTargetCapacityType *string `url:"DefaultTargetCapacityType"`
	OnDemandTargetCapacity    *int    `url:"OnDemandTargetCapacity"`
	SpotTargetCapacity        *int    `url:"SpotTargetCapacity"`
	TargetCapacityUnitType    *string `url:"TargetCapacityUnitType"`
	TotalTargetCapacity       *int    `url:"TotalTargetCapacity" validate:"required,gt=0"`
}

type CreateFleetRequest struct {
	CommonRequest
	DryRunnableRequest
	LaunchTemplateConfigs       []FleetLaunchTemplateConfigRequest  `url:"LaunchTemplateConfigs" validate:"required,min=1,dive"`
	TagSpecifications           []TagSpecification                  `url:"TagSpecification"`
	TargetCapacitySpecification *TargetCapacitySpecificationRequest `url:"TargetCapacitySpecification" validate:"required"`
	Type                        string                              `url:"Type"`
}

func (r CreateFleetRequest) Action() Action { return ActionCreateFleet }
