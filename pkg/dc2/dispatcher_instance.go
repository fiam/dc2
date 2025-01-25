package dc2

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	instanceIDPrefix = "i-"
)

func (d *Dispatcher) dispatchRunInstances(ctx context.Context, req *api.RunInstancesRequest) (*api.RunInstancesResponse, error) {
	if err := validateTagSpecifications(req.TagSpecifications, types.ResourceTypeInstance); err != nil {
		return nil, err
	}
	var availabilityZone string
	if req.Placement != nil && req.Placement.AvailabilityZone != "" {
		if err := validateAvailabilityZone(req.Placement.AvailabilityZone, d.opts.Region); err != nil {
			return nil, err
		}
		availabilityZone = req.Placement.AvailabilityZone
	} else {
		availabilityZone = defaultAvailabilityZone(d.opts.Region)
	}
	ids, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      req.ImageID,
		InstanceType: req.InstanceType,
		Count:        req.MaxCount,
	})
	if err != nil {
		return nil, executorError(err)
	}

	attrs := []storage.Attribute{
		{
			Key: attributeNameAvailabilityZone, Value: availabilityZone,
		},
	}
	if req.KeyName != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceKeyName, Value: req.KeyName})
	}
	for _, spec := range req.TagSpecifications {
		for _, tag := range spec.Tags {
			attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value})
		}
	}

	for _, executorID := range ids {
		id := string(instanceIDPrefix + executorID)
		r := storage.Resource{Type: types.ResourceTypeInstance, ID: id}
		if err := d.storage.RegisterResource(r); err != nil {
			return nil, fmt.Errorf("registering instance %s: %w", id, err)
		}
		if len(attrs) > 0 {
			if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
				return nil, fmt.Errorf("storing instance attributes: %w", err)
			}
		}
	}

	if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: ids,
	}); err != nil {
		return nil, executorError(err)
	}

	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	instances := make([]api.Instance, len(descriptions))
	for i, desc := range descriptions {
		instances[i], err = d.apiInstance(&desc)
		if err != nil {
			return nil, err
		}
	}
	return &api.RunInstancesResponse{
		InstancesSet: instances,
	}, nil
}

func (d *Dispatcher) dispatchDescribeInstances(ctx context.Context, req *api.DescribeInstancesRequest) (*api.DescribeInstancesResponse, error) {
	instanceIDs, err := d.applyFilters(types.ResourceTypeInstance, req.InstanceIDs, req.Filters)
	if err != nil {
		return nil, err
	}
	ids := executorInstanceIDs(instanceIDs)
	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	var reservations []api.Reservation
	if len(descriptions) > 0 {
		instances := make([]api.Instance, len(descriptions))
		for i, desc := range descriptions {
			instances[i], err = d.apiInstance(&desc)
			if err != nil {
				return nil, err
			}
		}
		reservations = append(reservations, api.Reservation{InstancesSet: instances})
	}
	return &api.DescribeInstancesResponse{
		ReservationSet: reservations,
	}, nil
}

func (d *Dispatcher) dispatchStopInstances(ctx context.Context, req *api.StopInstancesRequest) (*api.StopInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	ids := executorInstanceIDs(req.InstanceIDs)
	changes, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{
		InstanceIDs: ids,
		Force:       req.Force,
	})
	if err != nil {
		return nil, executorError(err)
	}
	return &api.StopInstancesResponse{
		StoppingInstances: apiInstanceChanges(changes),
	}, nil
}

func (d *Dispatcher) dispatchStartInstances(ctx context.Context, req *api.StartInstancesRequest) (*api.StartInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	ids := executorInstanceIDs(req.InstanceIDs)
	changes, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	return &api.StartInstancesResponse{
		StartingInstances: apiInstanceChanges(changes),
	}, nil
}

func (d *Dispatcher) dispatchTerminateInstances(ctx context.Context, req *api.TerminateInstancesRequest) (*api.TerminateInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	ids := executorInstanceIDs(req.InstanceIDs)
	changes, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	// TODO: remove resources from storage
	return &api.TerminateInstancesResponse{
		TerminatingInstances: apiInstanceChanges(changes),
	}, nil
}

func (d *Dispatcher) findInstance(ctx context.Context, instanceID string) (*storage.Resource, error) {
	instance, err := d.findResource(ctx, types.ResourceTypeInstance, instanceID)
	if err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.InvalidParameterValueError("InstanceId", instanceID)
		}
		return nil, err
	}
	return instance, nil
}

func (d *Dispatcher) apiInstance(desc *executor.InstanceDescription) (api.Instance, error) {
	instanceID := instanceIDPrefix + string(desc.InstanceID)
	attrs, err := d.storage.ResourceAttributes(instanceID)
	if err != nil {
		return api.Instance{}, fmt.Errorf("retrieving instance attributes: %w", err)
	}
	keyName, _ := attrs.Key(attributeNameInstanceKeyName)
	var tags []api.Tag
	for _, attr := range attrs {
		if attr.IsTag() {
			tags = append(tags, api.Tag{Key: attr.TagKey(), Value: attr.Value})
		}
	}
	availabilityZone, _ := attrs.Key(attributeNameAvailabilityZone)
	return api.Instance{
		InstanceID:     instanceID,
		ImageID:        desc.ImageID,
		InstanceState:  desc.InstanceState,
		PrivateDNSName: desc.PrivateDNSName,
		KeyName:        keyName,
		InstanceType:   desc.InstanceType,
		LaunchTime:     desc.LaunchTime,
		Architecture:   desc.Architecture,
		TagSet:         tags,
		Placement: api.Placement{
			AvailabilityZone: availabilityZone,
		},
	}, nil
}

func apiInstanceChanges(changes []executor.InstanceStateChange) []api.InstanceStateChange {
	apiChanges := make([]api.InstanceStateChange, len(changes))
	for i, c := range changes {
		apiChanges[i] = api.InstanceStateChange{
			InstanceID:    apiInstanceID(c.InstanceID),
			CurrentState:  c.CurrentState,
			PreviousState: c.PreviousState,
		}
	}
	return apiChanges
}

func executorInstanceIDs(instanceIDs []string) []executor.InstanceID {
	ids := make([]executor.InstanceID, len(instanceIDs))
	for i, id := range instanceIDs {
		ids[i] = executorInstanceID(id)
	}
	return ids
}

func executorInstanceID(instanceID string) executor.InstanceID {
	return executor.InstanceID(instanceID[len(instanceIDPrefix):])
}

func apiInstanceID(instanceID executor.InstanceID) string {
	return instanceIDPrefix + string(instanceID)
}

func defaultAvailabilityZone(region string) string {
	return region + "a"
}

func validateAvailabilityZone(az string, region string) error {
	if strings.HasPrefix(az, region) && len(az) == len(region)+1 {
		azName := az[len(az)-1]
		if azName >= 'a' && azName <= 'z' {
			return nil
		}
	}
	msg := fmt.Sprintf("availability zone %q is not valid for region %q", az, region)
	return api.InvalidParameterValueError("AvailabilityZone", msg)
}
