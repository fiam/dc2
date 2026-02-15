package dc2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/google/uuid"

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
	Region          string
	IMDSBackendPort int
	InstanceNetwork string
}

type Dispatcher struct {
	opts    DispatcherOptions
	exe     executor.Executor
	imds    *imdsController
	storage storage.Storage

	dispatchMu sync.Mutex

	eventCLI           *client.Client
	eventCancel        context.CancelFunc
	eventDone          chan struct{}
	eventReconcileDone chan struct{}
	eventNotifyCh      chan struct{}
	pendingInstanceMu  sync.Mutex
	pendingInstances   map[string]struct{}
}

func NewDispatcher(ctx context.Context, opts DispatcherOptions, imds *imdsController) (*Dispatcher, error) {
	if imds == nil {
		return nil, errors.New("nil IMDS controller")
	}
	exe, err := docker.NewExecutor(ctx, docker.ExecutorOptions{
		IMDSBackendPort: opts.IMDSBackendPort,
		InstanceNetwork: opts.InstanceNetwork,
	})
	if err != nil {
		return nil, fmt.Errorf("initializing executor: %w", err)
	}
	d := &Dispatcher{
		opts:    opts,
		exe:     exe,
		imds:    imds,
		storage: storage.NewMemoryStorage(),
	}

	eventCLI, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		slog.Warn("failed to initialize Docker events client for auto scaling reconciliation", "error", err)
		return d, nil
	}
	d.eventCLI = eventCLI
	d.pendingInstances = make(map[string]struct{})
	d.startInstanceLifecycleEventWatcher()

	return d, nil
}

func (d *Dispatcher) Close(ctx context.Context) error {
	var closeErr error
	if d.eventCancel != nil {
		d.eventCancel()
	}
	if d.eventDone != nil {
		select {
		case <-d.eventDone:
		case <-ctx.Done():
			closeErr = errors.Join(closeErr, fmt.Errorf("waiting for instance lifecycle event watcher: %w", ctx.Err()))
		}
	}
	if d.eventReconcileDone != nil {
		select {
		case <-d.eventReconcileDone:
		case <-ctx.Done():
			closeErr = errors.Join(closeErr, fmt.Errorf("waiting for instance lifecycle event reconciler: %w", ctx.Err()))
		}
	}
	if d.eventCLI != nil {
		if err := d.eventCLI.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("closing Docker events client: %w", err))
		}
	}
	if err := d.exe.Close(ctx); err != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("closing executor: %w", err))
	}
	return closeErr
}

func (d *Dispatcher) Dispatch(ctx context.Context, req api.Request) (api.Response, error) {
	d.dispatchMu.Lock()
	defer d.dispatchMu.Unlock()

	if err := d.reconcilePendingAutoScalingEvents(ctx); err != nil {
		return nil, err
	}
	var resp api.Response
	var err error
	switch req.Action() {
	case api.ActionRunInstances:
		resp, err = d.dispatchRunInstances(ctx, req.(*api.RunInstancesRequest))
	case api.ActionDescribeInstances:
		resp, err = d.dispatchDescribeInstances(ctx, req.(*api.DescribeInstancesRequest))
	case api.ActionDescribeInstanceStatus:
		resp, err = d.dispatchDescribeInstanceStatus(ctx, req.(*api.DescribeInstanceStatusRequest))
	case api.ActionStopInstances:
		resp, err = d.dispatchStopInstances(ctx, req.(*api.StopInstancesRequest))
	case api.ActionStartInstances:
		resp, err = d.dispatchStartInstances(ctx, req.(*api.StartInstancesRequest))
	case api.ActionTerminateInstances:
		resp, err = d.dispatchTerminateInstances(ctx, req.(*api.TerminateInstancesRequest))
	case api.ActionModifyInstanceMetadataOptions:
		resp, err = d.dispatchModifyInstanceMetadataOptions(ctx, req.(*api.ModifyInstanceMetadataOptionsRequest))
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
	case api.ActionCreateLaunchTemplate:
		resp, err = d.dispatchCreateLaunchTemplate(ctx, req.(*api.CreateLaunchTemplateRequest))
	case api.ActionDescribeLaunchTemplates:
		resp, err = d.dispatchDescribeLaunchTemplates(ctx, req.(*api.DescribeLaunchTemplatesRequest))
	case api.ActionDeleteLaunchTemplate:
		resp, err = d.dispatchDeleteLaunchTemplate(ctx, req.(*api.DeleteLaunchTemplateRequest))
	case api.ActionCreateLaunchTemplateVersion:
		resp, err = d.dispatchCreateLaunchTemplateVersion(ctx, req.(*api.CreateLaunchTemplateVersionRequest))
	case api.ActionDescribeLaunchTemplateVersions:
		resp, err = d.dispatchDescribeLaunchTemplateVersions(ctx, req.(*api.DescribeLaunchTemplateVersionsRequest))
	case api.ActionModifyLaunchTemplate:
		resp, err = d.dispatchModifyLaunchTemplate(ctx, req.(*api.ModifyLaunchTemplateRequest))
	case api.ActionCreateOrUpdateAutoScalingTags:
		resp, err = d.dispatchCreateOrUpdateAutoScalingTags(ctx, req.(*api.CreateOrUpdateAutoScalingTagsRequest))
	case api.ActionCreateAutoScalingGroup:
		resp, err = d.dispatchCreateAutoScalingGroup(ctx, req.(*api.CreateAutoScalingGroupRequest))
	case api.ActionDescribeAutoScalingGroups:
		resp, err = d.dispatchDescribeAutoScalingGroups(ctx, req.(*api.DescribeAutoScalingGroupsRequest))
	case api.ActionUpdateAutoScalingGroup:
		resp, err = d.dispatchUpdateAutoScalingGroup(ctx, req.(*api.UpdateAutoScalingGroupRequest))
	case api.ActionSetDesiredCapacity:
		resp, err = d.dispatchSetDesiredCapacity(ctx, req.(*api.SetDesiredCapacityRequest))
	case api.ActionDetachInstances:
		resp, err = d.dispatchDetachInstances(ctx, req.(*api.DetachInstancesRequest))
	case api.ActionDeleteAutoScalingGroup:
		resp, err = d.dispatchDeleteAutoScalingGroup(ctx, req.(*api.DeleteAutoScalingGroupRequest))
	default:
		return nil, api.ErrWithCode(api.ErrorCodeInvalidAction, fmt.Errorf("unhandled action %d", req.Action()))
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (d *Dispatcher) startInstanceLifecycleEventWatcher() {
	if d.eventCLI == nil {
		return
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	d.eventCancel = cancel
	d.eventDone = make(chan struct{})
	d.eventReconcileDone = make(chan struct{})
	d.eventNotifyCh = make(chan struct{}, 1)

	go func() {
		defer close(d.eventReconcileDone)
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-d.eventNotifyCh:
				d.dispatchMu.Lock()
				if watchCtx.Err() != nil {
					d.dispatchMu.Unlock()
					return
				}
				if err := d.reconcilePendingAutoScalingEvents(context.Background()); err != nil {
					slog.Warn("failed to reconcile auto scaling groups from Docker instance lifecycle events", "error", err)
				}
				d.dispatchMu.Unlock()
			}
		}
	}()

	go func() {
		defer close(d.eventDone)
		retryDelay := time.Second
		for {
			args := filters.NewArgs(
				filters.Arg("type", string(events.ContainerEventType)),
				filters.Arg("label", docker.LabelDC2Enabled+"=true"),
			)
			msgCh, errCh := d.eventCLI.Events(watchCtx, events.ListOptions{Filters: args})

			for {
				select {
				case <-watchCtx.Done():
					return
				case msg, ok := <-msgCh:
					if !ok {
						goto reconnect
					}
					if msg.Actor.ID == "" || !isAutoScalingReconcileEvent(msg) {
						continue
					}
					instanceID := apiInstanceID(executor.InstanceID(msg.Actor.ID))
					d.pendingInstanceMu.Lock()
					d.pendingInstances[instanceID] = struct{}{}
					d.pendingInstanceMu.Unlock()
					select {
					case d.eventNotifyCh <- struct{}{}:
					default:
					}
				case err, ok := <-errCh:
					if !ok {
						goto reconnect
					}
					if err != nil && !errors.Is(err, context.Canceled) {
						slog.Warn("Docker instance lifecycle event stream error", "error", err)
					}
					goto reconnect
				}
			}

		reconnect:
			if watchCtx.Err() != nil {
				return
			}
			timer := time.NewTimer(retryDelay)
			select {
			case <-watchCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if retryDelay < 5*time.Second {
				retryDelay *= 2
			}
		}
	}()
}

func isAutoScalingReconcileEvent(msg events.Message) bool {
	action := dockerEventAction(msg)
	switch action {
	case "destroy", "die", "stop":
		return true
	}
	if strings.HasPrefix(action, "health_status") {
		return dockerEventIsUnhealthy(msg, action)
	}
	return false
}

func dockerEventAction(msg events.Message) string {
	return strings.ToLower(strings.TrimSpace(string(msg.Action)))
}

func dockerEventIsUnhealthy(msg events.Message, action string) bool {
	if strings.Contains(action, "unhealthy") {
		return true
	}
	for _, key := range []string{"health_status", "health-status", "health"} {
		if strings.EqualFold(strings.TrimSpace(msg.Actor.Attributes[key]), "unhealthy") {
			return true
		}
	}
	return false
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
	if err := d.syncIMDSTagsForResources(req.ResourceIDs); err != nil {
		return nil, err
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
	if err := d.syncIMDSTagsForResources(req.ResourceIDs); err != nil {
		return nil, err
	}
	return &api.DeleteTagsResponse{}, nil
}

func (d *Dispatcher) syncIMDSTagsForResources(resourceIDs []string) error {
	for _, resourceID := range resourceIDs {
		if !strings.HasPrefix(resourceID, instanceIDPrefix) {
			continue
		}
		attrs, err := d.storage.ResourceAttributes(resourceID)
		if err != nil {
			return fmt.Errorf("retrieving attributes for %s: %w", resourceID, err)
		}
		tags := make(map[string]string)
		for _, attr := range attrs {
			if attr.IsTag() {
				tags[attr.TagKey()] = attr.Value
			}
		}
		if err := d.imds.SetTags(string(executorInstanceID(resourceID)), tags); err != nil {
			return fmt.Errorf("synchronizing IMDS tags for %s: %w", resourceID, err)
		}
	}
	return nil
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
						if slices.Contains(f.Values, attr.Value) {
							filtered = append(filtered, id)
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

func applyNextToken[E any](elems []E, nextToken *string, maxResults *int) ([]E, *string, error) {
	const (
		base = 36
	)
	offset := 0
	if nextToken != nil {
		o, err := strconv.ParseInt(*nextToken, base, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid NextToken %q: %w", *nextToken, err)
		}
		offset = int(o)
	}
	if offset > 0 {
		offset = min(offset, len(elems))
		elems = elems[offset:]
	}
	var nextNextToken *string
	if maxResults != nil && len(elems) > *maxResults {
		elems = elems[:*maxResults]
		t := strconv.FormatInt(int64(offset+*maxResults), base)
		nextNextToken = &t
	}
	return elems, nextNextToken, nil
}

func makeID(prefix string) (string, error) {
	u, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("initializing resource ID: %w", err)
	}
	return prefix + u.String(), nil
}
