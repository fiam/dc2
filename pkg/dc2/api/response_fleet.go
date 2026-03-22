package api

type FleetLaunchTemplateSpecificationResponse struct {
	LaunchTemplateID   *string `xml:"launchTemplateId"`
	LaunchTemplateName *string `xml:"launchTemplateName"`
	Version            *string `xml:"version"`
}

type FleetPlacementResponse struct {
	GroupName *string `xml:"groupName"`
}

type FleetLaunchTemplateOverridesResponse struct {
	AvailabilityZone *string                 `xml:"availabilityZone"`
	ImageID          *string                 `xml:"imageId"`
	InstanceType     *string                 `xml:"instanceType"`
	Placement        *FleetPlacementResponse `xml:"placement"`
	SubnetID         *string                 `xml:"subnetId"`
}

type LaunchTemplateAndOverridesResponse struct {
	LaunchTemplateSpecification *FleetLaunchTemplateSpecificationResponse `xml:"launchTemplateSpecification"`
	Overrides                   *FleetLaunchTemplateOverridesResponse     `xml:"overrides"`
}

type CreateFleetInstance struct {
	InstanceIDs                []string                            `xml:"instanceIds>item"`
	InstanceType               string                              `xml:"instanceType"`
	LaunchTemplateAndOverrides *LaunchTemplateAndOverridesResponse `xml:"launchTemplateAndOverrides"`
}

type CreateFleetError struct {
	ErrorCode                  *string                             `xml:"errorCode"`
	ErrorMessage               *string                             `xml:"errorMessage"`
	LaunchTemplateAndOverrides *LaunchTemplateAndOverridesResponse `xml:"launchTemplateAndOverrides"`
}

type CreateFleetResponse struct {
	Errors    []CreateFleetError    `xml:"errorSet>item"`
	FleetID   *string               `xml:"fleetId"`
	Instances []CreateFleetInstance `xml:"fleetInstanceSet>item"`
}
