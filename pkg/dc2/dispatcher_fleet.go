package dc2

import (
	"context"
	"fmt"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
)

const (
	createFleetTypeInstant           = "instant"
	createFleetCapacityTypeOnDemand  = "on-demand"
	createFleetCapacityUnitTypeUnits = "units"
)

func (d *Dispatcher) dispatchCreateFleet(ctx context.Context, req *api.CreateFleetRequest) (*api.CreateFleetResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	count, err := validateCreateFleetTargetCapacity(req.TargetCapacitySpecification)
	if err != nil {
		return nil, err
	}
	if err := validateCreateFleetType(req.Type); err != nil {
		return nil, err
	}
	if len(req.LaunchTemplateConfigs) != 1 {
		return nil, api.InvalidParameterValueError(
			"LaunchTemplateConfigs",
			fmt.Sprintf("length %d", len(req.LaunchTemplateConfigs)),
		)
	}
	if err := validateRunInstancesTagSpecifications(req.TagSpecifications, ""); err != nil {
		return nil, err
	}

	runReq, launchTemplateAndOverrides, err := d.createFleetRunInstancesRequest(ctx, req, count)
	if err != nil {
		return nil, err
	}

	runResp, err := d.dispatchRunInstances(ctx, runReq)
	if err != nil {
		return nil, err
	}

	instanceIDs := make([]string, 0, len(runResp.InstancesSet))
	instanceType := runReq.InstanceType
	if instanceType == "" && len(runResp.InstancesSet) > 0 {
		instanceType = runResp.InstancesSet[0].InstanceType
	}
	for _, instance := range runResp.InstancesSet {
		instanceIDs = append(instanceIDs, instance.InstanceID)
	}

	return &api.CreateFleetResponse{
		Instances: []api.CreateFleetInstance{
			{
				InstanceIDs:                instanceIDs,
				InstanceType:               instanceType,
				LaunchTemplateAndOverrides: launchTemplateAndOverrides,
			},
		},
	}, nil
}

func validateCreateFleetType(rawType string) error {
	if !strings.EqualFold(strings.TrimSpace(rawType), createFleetTypeInstant) {
		return api.InvalidParameterValueError("Type", rawType)
	}
	return nil
}

func validateCreateFleetTargetCapacity(spec *api.TargetCapacitySpecificationRequest) (int, error) {
	if spec == nil || spec.TotalTargetCapacity == nil {
		return 0, api.ErrWithCode("ValidationError", fmt.Errorf("TargetCapacitySpecification.TotalTargetCapacity is required"))
	}
	if *spec.TotalTargetCapacity <= 0 {
		return 0, api.InvalidParameterValueError(
			"TargetCapacitySpecification.TotalTargetCapacity",
			fmt.Sprintf("%d", *spec.TotalTargetCapacity),
		)
	}
	if spec.DefaultTargetCapacityType != nil {
		defaultType := strings.TrimSpace(*spec.DefaultTargetCapacityType)
		if defaultType != "" && !strings.EqualFold(defaultType, createFleetCapacityTypeOnDemand) {
			return 0, api.InvalidParameterValueError("TargetCapacitySpecification.DefaultTargetCapacityType", defaultType)
		}
	}
	if spec.TargetCapacityUnitType != nil {
		unitType := strings.TrimSpace(*spec.TargetCapacityUnitType)
		if unitType != "" && !strings.EqualFold(unitType, createFleetCapacityUnitTypeUnits) {
			return 0, api.InvalidParameterValueError("TargetCapacitySpecification.TargetCapacityUnitType", unitType)
		}
	}
	if spec.SpotTargetCapacity != nil && *spec.SpotTargetCapacity != 0 {
		return 0, api.InvalidParameterValueError(
			"TargetCapacitySpecification.SpotTargetCapacity",
			fmt.Sprintf("%d", *spec.SpotTargetCapacity),
		)
	}
	if spec.OnDemandTargetCapacity != nil && *spec.OnDemandTargetCapacity != *spec.TotalTargetCapacity {
		return 0, api.InvalidParameterValueError(
			"TargetCapacitySpecification.OnDemandTargetCapacity",
			fmt.Sprintf("%d", *spec.OnDemandTargetCapacity),
		)
	}
	return *spec.TotalTargetCapacity, nil
}

func (d *Dispatcher) createFleetRunInstancesRequest(
	ctx context.Context,
	req *api.CreateFleetRequest,
	count int,
) (*api.RunInstancesRequest, *api.LaunchTemplateAndOverridesResponse, error) {
	config := req.LaunchTemplateConfigs[0]
	if config.LaunchTemplateSpecification == nil {
		return nil, nil, api.ErrWithCode("ValidationError", fmt.Errorf("LaunchTemplateConfigs.1.LaunchTemplateSpecification is required"))
	}
	if len(config.Overrides) > 1 {
		return nil, nil, api.InvalidParameterValueError(
			"LaunchTemplateConfigs.1.Overrides",
			fmt.Sprintf("length %d", len(config.Overrides)),
		)
	}

	launchTemplateSpec := createFleetLaunchTemplateSpecification(config.LaunchTemplateSpecification)
	lt, err := d.findLaunchTemplate(ctx, launchTemplateSpec)
	if err != nil {
		return nil, nil, err
	}

	resolvedInstanceType, overrideResponse, err := d.resolveCreateFleetInstanceType(lt, config.Overrides)
	if err != nil {
		return nil, nil, err
	}

	runReq := &api.RunInstancesRequest{
		CommonRequest: api.CommonRequest{
			Action:      req.CommonRequest.Action,
			Version:     req.CommonRequest.Version,
			ClientToken: req.CommonRequest.ClientToken,
		},
		ImageID:      lt.ImageID,
		InstanceType: resolvedInstanceType,
		LaunchTemplate: &api.AutoScalingLaunchTemplateSpecification{
			LaunchTemplateID:   stringPtr(lt.ID),
			LaunchTemplateName: stringPtr(lt.Name),
			Version:            stringPtr(lt.Version),
		},
		MinCount:          count,
		MaxCount:          count,
		TagSpecifications: req.TagSpecifications,
	}

	if len(config.Overrides) > 0 {
		override := config.Overrides[0]
		if override.ImageID != nil && strings.TrimSpace(*override.ImageID) != "" {
			runReq.ImageID = strings.TrimSpace(*override.ImageID)
		}
		if override.SubnetID != nil && strings.TrimSpace(*override.SubnetID) != "" {
			runReq.SubnetID = strings.TrimSpace(*override.SubnetID)
		}
		if override.AvailabilityZone != nil && strings.TrimSpace(*override.AvailabilityZone) != "" {
			runReq.Placement = &api.Placement{
				AvailabilityZone: strings.TrimSpace(*override.AvailabilityZone),
			}
		}
	}

	launchTemplateAndOverrides := &api.LaunchTemplateAndOverridesResponse{
		LaunchTemplateSpecification: &api.FleetLaunchTemplateSpecificationResponse{
			LaunchTemplateID:   stringPtr(lt.ID),
			LaunchTemplateName: stringPtr(lt.Name),
			Version:            stringPtr(lt.Version),
		},
		Overrides: overrideResponse,
	}

	return runReq, launchTemplateAndOverrides, nil
}

func createFleetLaunchTemplateSpecification(
	spec *api.FleetLaunchTemplateSpecificationRequest,
) *api.AutoScalingLaunchTemplateSpecification {
	if spec == nil {
		return nil
	}
	return &api.AutoScalingLaunchTemplateSpecification{
		LaunchTemplateID:   spec.LaunchTemplateID,
		LaunchTemplateName: spec.LaunchTemplateName,
		Version:            spec.Version,
	}
}

func (d *Dispatcher) resolveCreateFleetInstanceType(
	lt *launchTemplateData,
	overrides []api.FleetLaunchTemplateOverridesRequest,
) (string, *api.FleetLaunchTemplateOverridesResponse, error) {
	if len(overrides) == 0 {
		instanceType, err := d.resolveCreateFleetLaunchTemplateInstanceType(lt)
		return instanceType, nil, err
	}

	override := overrides[0]
	response := &api.FleetLaunchTemplateOverridesResponse{
		AvailabilityZone: override.AvailabilityZone,
		ImageID:          override.ImageID,
		SubnetID:         override.SubnetID,
	}
	if override.Placement != nil {
		response.Placement = &api.FleetPlacementResponse{
			GroupName: override.Placement.GroupName,
		}
	}
	if override.InstanceType != nil && strings.TrimSpace(*override.InstanceType) != "" {
		instanceType := strings.TrimSpace(*override.InstanceType)
		response.InstanceType = stringPtr(instanceType)
		return instanceType, response, nil
	}
	if override.InstanceRequirements != nil {
		instanceType, err := d.resolveAutoScalingInstanceTypeFromRequirements(override.InstanceRequirements)
		if err != nil {
			return "", nil, err
		}
		response.InstanceType = stringPtr(instanceType)
		return instanceType, response, nil
	}

	instanceType, err := d.resolveCreateFleetLaunchTemplateInstanceType(lt)
	if err != nil {
		return "", nil, err
	}
	response.InstanceType = stringPtr(instanceType)
	return instanceType, response, nil
}

func (d *Dispatcher) resolveCreateFleetLaunchTemplateInstanceType(lt *launchTemplateData) (string, error) {
	switch {
	case lt == nil:
		return "", api.ErrWithCode("ValidationError", fmt.Errorf("launch template is required"))
	case strings.TrimSpace(lt.InstanceType) != "":
		return strings.TrimSpace(lt.InstanceType), nil
	case lt.InstanceRequirements != nil:
		return d.resolveAutoScalingInstanceTypeFromRequirements(lt.InstanceRequirements)
	default:
		return "", api.ErrWithCode(
			"ValidationError",
			fmt.Errorf("launch template must define ImageId and a resolvable InstanceType"),
		)
	}
}
