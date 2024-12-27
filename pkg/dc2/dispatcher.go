package dc2

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/docker"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	attributeNameInstanceKeyName = "KeyName"
	tagRequestCountLimit         = 1000
)

type Dispatcher struct {
	exe     executor.Executor
	storage storage.Storage
}

func NewDispatcher() (*Dispatcher, error) {
	exe, err := docker.NewExecutor()
	if err != nil {
		return nil, fmt.Errorf("initializing executor: %w", err)
	}
	return &Dispatcher{
		exe:     exe,
		storage: storage.NewMemoryStorage(),
	}, nil
}

func (d *Dispatcher) Dispatch(ctx context.Context, req api.Request) (api.Response, error) {
	switch req.Action() {
	case api.ActionRunInstances:
		resp, err := d.dispatchRunInstances(ctx, req.(*api.RunInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionDescribeInstances:
		resp, err := d.dispatchDescribeInstances(ctx, req.(*api.DescribeInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionStopInstances:
		resp, err := d.dispatchStopInstances(ctx, req.(*api.StopInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionStartInstances:
		resp, err := d.dispatchStartInstances(ctx, req.(*api.StartInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionTerminateInstances:
		resp, err := d.dispatchTerminateInstances(ctx, req.(*api.TerminateInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionCreateTags:
		resp, err := d.dispatchCreateTags(ctx, req.(*api.CreateTagsRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionDeleteTags:
		resp, err := d.dispatchDeleteTags(ctx, req.(*api.DeleteTagsRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	return nil, api.ErrWithCode(api.ErrorCodeInvalidAction, fmt.Errorf("unhandled action %d", req.Action()))
}

func (d *Dispatcher) dispatchRunInstances(ctx context.Context, req *api.RunInstancesRequest) (*api.RunInstancesResponse, error) {
	if err := validateTagSpecifications(req.TagSpecifications, types.ResourceTypeInstance); err != nil {
		return nil, err
	}
	instanceIDs, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      req.ImageID,
		InstanceType: req.InstanceType,
		Count:        req.MaxCount,
	})
	if err != nil {
		return nil, executorError(err)
	}

	var attrs []storage.Attribute
	if req.KeyName != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceKeyName, Value: req.KeyName})
	}
	for _, spec := range req.TagSpecifications {
		for _, tag := range spec.Tags {
			attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value})
		}
	}

	for _, id := range instanceIDs {
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
		InstanceIDs: instanceIDs,
	}); err != nil {
		return nil, executorError(err)
	}

	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: instanceIDs,
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
	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: instanceIDs,
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
	changes, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{
		InstanceIDs: req.InstanceIDs,
		Force:       req.Force,
	})
	if err != nil {
		return nil, executorError(err)
	}
	return &api.StopInstancesResponse{
		StoppingInstances: changes,
	}, nil
}

func (d *Dispatcher) dispatchStartInstances(ctx context.Context, req *api.StartInstancesRequest) (*api.StartInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	changes, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: req.InstanceIDs,
	})
	if err != nil {
		return nil, executorError(err)
	}
	return &api.StartInstancesResponse{
		StartingInstances: changes,
	}, nil
}

func (d *Dispatcher) dispatchTerminateInstances(ctx context.Context, req *api.TerminateInstancesRequest) (*api.TerminateInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	changes, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{
		InstanceIDs: req.InstanceIDs,
	})
	if err != nil {
		return nil, executorError(err)
	}
	// TODO: remove resources from storage
	return &api.TerminateInstancesResponse{
		TerminatingInstances: changes,
	}, nil
}

func (d *Dispatcher) dispatchCreateTags(_ context.Context, req *api.CreateTagsRequest) (*api.CreateTagsResponse, error) {
	if len(req.Tags) > tagRequestCountLimit {
		return nil, api.InvalidParameterValueError("Tags", fmt.Sprintf("length %d exceeds limit %d", len(req.Tags), tagRequestCountLimit))
	}
	if req.DryRun {
		return nil, api.DryRunError()
	}
	attrs := make([]storage.Attribute, len(req.Tags))
	for i, tag := range req.Tags {
		attrs[i] = storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value}
	}
	for _, id := range req.ResourceIDs {
		if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
			return nil, fmt.Errorf("setting resource attributes for %s: %w", id, err)
		}
	}
	return &api.CreateTagsResponse{}, nil
}

func (d *Dispatcher) dispatchDeleteTags(_ context.Context, req *api.DeleteTagsRequest) (*api.DeleteTagsResponse, error) {
	if len(req.Tags) > tagRequestCountLimit {
		return nil, api.InvalidParameterValueError("Tags", fmt.Sprintf("length %d exceeds limit %d", len(req.Tags), tagRequestCountLimit))
	}
	if req.DryRun {
		return nil, api.DryRunError()
	}
	attrs := make([]storage.Attribute, len(req.Tags))
	for i, tag := range req.Tags {
		attrs[i] = storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value}
	}
	for _, id := range req.ResourceIDs {
		if err := d.storage.RemoveResourceAttributes(id, attrs); err != nil {
			return nil, fmt.Errorf("removing resource attributes for %s: %w", id, err)
		}
	}
	return &api.DeleteTagsResponse{}, nil
}

func (d *Dispatcher) apiInstance(desc *executor.InstanceDescription) (api.Instance, error) {
	attrs, err := d.storage.ResourceAttributes(desc.InstanceID)
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
	return api.Instance{
		InstanceID:     desc.InstanceID,
		ImageID:        desc.ImageID,
		InstanceState:  desc.InstanceState,
		PrivateDNSName: desc.PrivateDNSName,
		KeyName:        keyName,
		InstanceType:   desc.InstanceType,
		LaunchTime:     desc.LaunchTime,
		Architecture:   desc.Architecture,
		TagSet:         tags,
	}, nil
}

func (d *Dispatcher) applyFilters(resourceType types.ResourceType, initialIDs []string, filters []api.Filter) ([]string, error) {
	ids := initialIDs
	if len(ids) == 0 {
		rs, err := d.storage.RegisteredResources(resourceType)
		if err != nil {
			return nil, fmt.Errorf("retrieving registered resources: %w", err)
		}
		ids = make([]string, len(rs))
		for i, r := range rs {
			ids[i] = r.ID
		}
	}
	resourceAttributes := make(map[string]storage.Attributes)
	for _, id := range ids {
		attrs, err := d.storage.ResourceAttributes(id)
		if err != nil {
			return nil, fmt.Errorf("retrieving resource attributes: %w", err)
		}
		resourceAttributes[id] = attrs
	}
	for _, f := range filters {
		var filtered []string
		if f.Name == nil {
			return nil, api.InvalidParameterValueError("Filter.Name", "<missing>")
		}
		if *f.Name == "" {
			return nil, api.InvalidParameterValueError("Filter.Name", "<empty>")
		}
		if f.Values == nil {
			return nil, api.InvalidParameterValueError("Filter.Values", "<missing>")
		}
		switch {
		case strings.HasPrefix(*f.Name, "tag:"):
			tagKey := (*f.Name)[4:]
			for _, id := range ids {
				for _, attr := range resourceAttributes[id] {
					if attr.IsTag() && attr.TagKey() == tagKey {
						for _, v := range f.Values {
							if attr.Value == v {
								filtered = append(filtered, id)
								break
							}
						}
					}
				}
			}
		case *f.Name == "tag-key":
			for _, id := range ids {
				for _, attr := range resourceAttributes[id] {
					if attr.IsTag() && slices.Contains(f.Values, attr.TagKey()) {
						filtered = append(filtered, id)
						break
					}
				}
			}
		default:
			return nil, api.InvalidParameterValueError("Filter.Name", *f.Name)
		}
		ids = filtered
	}
	return ids, nil
}

func executorError(err error) error {
	return fmt.Errorf("executor returned an error: %w", err)
}

func validateTagSpecifications(specs []api.TagSpecification, requiredResourceType types.ResourceType) error {
	for i, tagSpec := range specs {
		if tagSpec.ResourceType != requiredResourceType {
			return api.InvalidParameterValueError(fmt.Sprintf("TagSpecification.%d.ResourceType", i+1), string(tagSpec.ResourceType))
		}
	}
	return nil
}
