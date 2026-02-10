package dc2

import (
	"context"
	"fmt"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	launchTemplateIDPrefix = "lt-"
)

func (d *Dispatcher) dispatchCreateLaunchTemplate(ctx context.Context, req *api.CreateLaunchTemplateRequest) (*api.CreateLaunchTemplateResponse, error) {
	if req.LaunchTemplateData.InstanceType == "" && len(req.LaunchTemplateData.TagSpecifications) == 0 {
		return nil, api.InvalidParameterValueError("LaunchTemplateData", "<empty>")
	}
	if err := validateLaunchTemplateTagSpecifications(req.LaunchTemplateData.TagSpecifications); err != nil {
		return nil, err
	}

	launchTemplateID, err := makeID(launchTemplateIDPrefix)
	if err != nil {
		return nil, err
	}

	err = d.storage.RegisterResource(storage.Resource{
		Type: types.ResourceTypeLaunchTemplate,
		ID:   launchTemplateID,
	})
	if err != nil {
		return nil, fmt.Errorf("registering launch template: %w", err)
	}

	launchTemplateName := req.LaunchTemplateName
	createTime := time.Now().UTC()
	defaultVersionNumber := int64(1)
	latestVersionNumber := int64(1)

	return &api.CreateLaunchTemplateResponse{
		LaunchTemplate: &api.LaunchTemplate{
			CreateTime:           &createTime,
			DefaultVersionNumber: &defaultVersionNumber,
			LatestVersionNumber:  &latestVersionNumber,
			LaunchTemplateId:     &launchTemplateID,
			LaunchTemplateName:   &launchTemplateName,
		},
	}, nil

}

func validateLaunchTemplateTagSpecifications(specs []api.TagSpecification) error {
	for i, spec := range specs {
		switch spec.ResourceType {
		case types.ResourceTypeInstance,
			types.ResourceTypeVolume,
			types.ResourceTypeNetworkInterface,
			types.ResourceTypeSpotInstancesRequest:
		default:
			param := fmt.Sprintf("LaunchTemplateData.TagSpecification.%d.ResourceType", i+1)
			return api.InvalidParameterValueError(param, string(spec.ResourceType))
		}
	}
	return nil
}
