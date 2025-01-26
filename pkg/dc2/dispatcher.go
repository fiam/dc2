package dc2

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/docker"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	attributeNameInstanceKeyName  = "KeyName"
	attributeNameAvailabilityZone = "AvailabilityZone"
	attributeNameCreateTime       = "CreateTime"
	tagRequestCountLimit          = 1000
)

type DispatcherOptions struct {
	Region string
}

type Dispatcher struct {
	opts    DispatcherOptions
	exe     executor.Executor
	storage storage.Storage
}

func NewDispatcher(ctx context.Context, opts DispatcherOptions) (*Dispatcher, error) {
	exe, err := docker.NewExecutor(ctx)
	if err != nil {
		return nil, fmt.Errorf("initializing executor: %w", err)
	}
	return &Dispatcher{
		opts:    opts,
		exe:     exe,
		storage: storage.NewMemoryStorage(),
	}, nil
}

func (d *Dispatcher) Close(ctx context.Context) error {
	if err := d.exe.Close(ctx); err != nil {
		return fmt.Errorf("closing executor: %w", err)
	}
	return nil
}

func (d *Dispatcher) Dispatch(ctx context.Context, req api.Request) (api.Response, error) {
	var resp api.Response
	var err error
	switch req.Action() {
	case api.ActionRunInstances:
		resp, err = d.dispatchRunInstances(ctx, req.(*api.RunInstancesRequest))
	case api.ActionDescribeInstances:
		resp, err = d.dispatchDescribeInstances(ctx, req.(*api.DescribeInstancesRequest))
	case api.ActionStopInstances:
		resp, err = d.dispatchStopInstances(ctx, req.(*api.StopInstancesRequest))
	case api.ActionStartInstances:
		resp, err = d.dispatchStartInstances(ctx, req.(*api.StartInstancesRequest))
	case api.ActionTerminateInstances:
		resp, err = d.dispatchTerminateInstances(ctx, req.(*api.TerminateInstancesRequest))
	case api.ActionCreateTags:
		resp, err = d.dispatchCreateTags(ctx, req.(*api.CreateTagsRequest))
	case api.ActionDeleteTags:
		resp, err = d.dispatchDeleteTags(ctx, req.(*api.DeleteTagsRequest))
	case api.ActionCreateVolume:
		resp, err = d.dispatchCreateVolume(ctx, req.(*api.CreateVolumeRequest))
	case api.ActionDeleteVolume:
		resp, err = d.dispatchDeleteVolume(ctx, req.(*api.DeleteVolumeRequest))
	case api.ActionAttachVolume:
		resp, err = d.dispatchAttachVolume(ctx, req.(*api.AttachVolumeRequest))
	case api.ActionDetachVolume:
		resp, err = d.dispatchDetachVolume(ctx, req.(*api.DetachVolumeRequest))
	case api.ActionDescribeVolumes:
		resp, err = d.dispatchDescribeVolumes(ctx, req.(*api.DescribeVolumesRequest))
	default:
		return nil, api.ErrWithCode(api.ErrorCodeInvalidAction, fmt.Errorf("unhandled action %d", req.Action()))
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
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

func (d *Dispatcher) findResource(_ context.Context, rt types.ResourceType, id string) (*storage.Resource, error) {
	resources, err := d.storage.RegisteredResources(rt)
	if err != nil {
		return nil, fmt.Errorf("retrieving registered %s: %w", rt, err)
	}
	for _, r := range resources {
		if r.ID == id {
			return &r, nil
		}
	}
	return nil, storage.ErrResourceNotFound{ID: id}
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

func parseTime(timeStr string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, timeStr)
}

func parseAttr[T any](attrs storage.Attributes, key string, parser func(string) (T, error)) (T, error) {
	val, _ := attrs.Key(key)
	return parser(val)
}
