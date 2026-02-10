package dc2

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	launchTemplateIDPrefix = "lt-"

	attributeNameLaunchTemplateName           = "LaunchTemplateName"
	attributeNameLaunchTemplateImageID        = "LaunchTemplateDataImageID"
	attributeNameLaunchTemplateInstanceType   = "LaunchTemplateDataInstanceType"
	attributeNameLaunchTemplateDefaultVersion = "LaunchTemplateDefaultVersion"
	attributeNameLaunchTemplateLatestVersion  = "LaunchTemplateLatestVersion"
)

func (d *Dispatcher) dispatchCreateLaunchTemplate(ctx context.Context, req *api.CreateLaunchTemplateRequest) (*api.CreateLaunchTemplateResponse, error) {
	if req.LaunchTemplateData.ImageID == "" &&
		req.LaunchTemplateData.InstanceType == "" &&
		len(req.LaunchTemplateData.TagSpecifications) == 0 {
		return nil, api.InvalidParameterValueError("LaunchTemplateData", "<empty>")
	}
	if err := validateLaunchTemplateTagSpecifications(req.LaunchTemplateData.TagSpecifications); err != nil {
		return nil, err
	}
	if _, err := d.findLaunchTemplateByName(ctx, req.LaunchTemplateName); err == nil {
		return nil, api.ErrWithCode("AlreadyExists", fmt.Errorf("launch template %q already exists", req.LaunchTemplateName))
	} else if !errors.As(err, &storage.ErrResourceNotFound{}) {
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
	attrs := []storage.Attribute{
		{Key: attributeNameLaunchTemplateName, Value: req.LaunchTemplateName},
		{Key: attributeNameLaunchTemplateDefaultVersion, Value: "1"},
		{Key: attributeNameLaunchTemplateLatestVersion, Value: "1"},
	}
	if req.LaunchTemplateData.ImageID != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameLaunchTemplateImageID, Value: req.LaunchTemplateData.ImageID})
	}
	if req.LaunchTemplateData.InstanceType != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameLaunchTemplateInstanceType, Value: req.LaunchTemplateData.InstanceType})
	}
	if err := d.storage.SetResourceAttributes(launchTemplateID, attrs); err != nil {
		return nil, fmt.Errorf("saving launch template attributes: %w", err)
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

type launchTemplateData struct {
	ID           string
	Name         string
	Version      string
	ImageID      string
	InstanceType string
}

func (d *Dispatcher) findLaunchTemplate(ctx context.Context, spec *api.AutoScalingLaunchTemplateSpecification) (*launchTemplateData, error) {
	if spec == nil {
		return nil, api.ErrWithCode("ValidationError", fmt.Errorf("LaunchTemplate is required"))
	}
	switch {
	case spec.LaunchTemplateID != nil && *spec.LaunchTemplateID != "":
		data, err := d.findLaunchTemplateByID(ctx, *spec.LaunchTemplateID)
		if err != nil {
			return nil, err
		}
		if spec.Version != nil && *spec.Version != "" {
			data.Version = *spec.Version
		}
		return data, nil
	case spec.LaunchTemplateName != nil && *spec.LaunchTemplateName != "":
		data, err := d.findLaunchTemplateByName(ctx, *spec.LaunchTemplateName)
		if err != nil {
			return nil, err
		}
		if spec.Version != nil && *spec.Version != "" {
			data.Version = *spec.Version
		}
		return data, nil
	default:
		return nil, api.ErrWithCode("ValidationError", fmt.Errorf("either LaunchTemplateId or LaunchTemplateName must be set"))
	}
}

func (d *Dispatcher) findLaunchTemplateByID(ctx context.Context, launchTemplateID string) (*launchTemplateData, error) {
	r, err := d.findResource(ctx, types.ResourceTypeLaunchTemplate, launchTemplateID)
	if err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("launch template %q was not found", launchTemplateID))
		}
		return nil, err
	}
	return d.loadLaunchTemplateData(r.ID)
}

func (d *Dispatcher) findLaunchTemplateByName(ctx context.Context, launchTemplateName string) (*launchTemplateData, error) {
	templates, err := d.storage.RegisteredResources(types.ResourceTypeLaunchTemplate)
	if err != nil {
		return nil, fmt.Errorf("retrieving launch templates: %w", err)
	}
	for _, r := range templates {
		data, err := d.loadLaunchTemplateData(r.ID)
		if err != nil {
			return nil, err
		}
		if data.Name == launchTemplateName {
			return data, nil
		}
	}
	return nil, storage.ErrResourceNotFound{ID: launchTemplateName}
}

func (d *Dispatcher) loadLaunchTemplateData(launchTemplateID string) (*launchTemplateData, error) {
	attrs, err := d.storage.ResourceAttributes(launchTemplateID)
	if err != nil {
		return nil, fmt.Errorf("retrieving launch template attributes: %w", err)
	}
	name, _ := attrs.Key(attributeNameLaunchTemplateName)
	if name == "" {
		return nil, fmt.Errorf("launch template %s missing name", launchTemplateID)
	}
	version, _ := attrs.Key(attributeNameLaunchTemplateDefaultVersion)
	if version == "" {
		version = "1"
	}
	imageID, _ := attrs.Key(attributeNameLaunchTemplateImageID)
	instanceType, _ := attrs.Key(attributeNameLaunchTemplateInstanceType)
	return &launchTemplateData{
		ID:           launchTemplateID,
		Name:         name,
		Version:      version,
		ImageID:      imageID,
		InstanceType: instanceType,
	}, nil
}
