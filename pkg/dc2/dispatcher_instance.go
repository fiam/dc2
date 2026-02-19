package dc2

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/testprofile"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	instanceIDPrefix = "i-"

	attributeNameInstanceUserData      = "UserData"
	attributeNameStateTransitionReason = "StateTransitionReason"
	attributeNameStateReasonCode       = "StateReasonCode"
	attributeNameStateReasonMessage    = "StateReasonMessage"
	attributeNameInstanceTerminatedAt  = "TerminatedAt"

	imdsEndpointEnabled       = "enabled"
	imdsEndpointDisabled      = "disabled"
	imdsStateApplied          = "applied"
	terminatedInstanceTTL     = 3 * time.Second
	stateReasonUserInitiated  = "Client.UserInitiatedShutdown"
	stateMessageUserInitiated = "Client.UserInitiatedShutdown: User initiated shutdown"
)

func (d *Dispatcher) dispatchRunInstances(ctx context.Context, req *api.RunInstancesRequest) (*api.RunInstancesResponse, error) {
	if err := validateTagSpecifications(req.TagSpecifications, types.ResourceTypeInstance); err != nil {
		return nil, err
	}
	if err := validateBlockDeviceMappings(req.BlockDeviceMappings, "BlockDeviceMapping"); err != nil {
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
	if err := d.applyRunInstancesDelay(ctx, testprofile.HookBefore, testprofile.PhaseAllocate, req); err != nil {
		return nil, err
	}
	ids, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      req.ImageID,
		InstanceType: req.InstanceType,
		Count:        req.MaxCount,
		UserData:     normalizeUserData(req.UserData),
	})
	if err != nil {
		return nil, executorError(err)
	}
	if err := d.applyRunInstancesDelay(ctx, testprofile.HookAfter, testprofile.PhaseAllocate, req); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, err
	}

	attrs := []storage.Attribute{
		{
			Key: attributeNameAvailabilityZone, Value: availabilityZone,
		},
	}
	if req.KeyName != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceKeyName, Value: req.KeyName})
	}
	if req.UserData != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceUserData, Value: normalizeUserData(req.UserData)})
	}
	instanceTags := make(map[string]string)
	for _, spec := range req.TagSpecifications {
		for _, tag := range spec.Tags {
			instanceTags[tag.Key] = tag.Value
			attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value})
		}
	}

	for _, executorID := range ids {
		id := string(instanceIDPrefix + executorID)
		r := storage.Resource{Type: types.ResourceTypeInstance, ID: id}
		if err := d.storage.RegisterResource(r); err != nil {
			d.cleanupFailedRunInstancesLaunch(ctx, ids)
			return nil, fmt.Errorf("registering instance %s: %w", id, err)
		}
		if len(attrs) > 0 {
			if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
				d.cleanupFailedRunInstancesLaunch(ctx, ids)
				return nil, fmt.Errorf("storing instance attributes: %w", err)
			}
		}
		if err := d.imds.SetTags(string(executorID), instanceTags); err != nil {
			d.cleanupFailedRunInstancesLaunch(ctx, ids)
			return nil, fmt.Errorf("synchronizing IMDS tags for instance %s: %w", id, err)
		}
	}

	if err := d.applyRunInstancesDelay(ctx, testprofile.HookBefore, testprofile.PhaseStart, req); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, err
	}
	if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: ids,
	}); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, executorError(err)
	}
	if err := d.applyRunInstancesDelay(ctx, testprofile.HookAfter, testprofile.PhaseStart, req); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, err
	}
	if err := d.attachInstanceBlockDeviceMappings(ctx, ids, availabilityZone, req.BlockDeviceMappings); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, err
	}

	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, executorError(err)
	}
	instances := make([]api.Instance, len(descriptions))
	createdInstanceIDs := make([]string, 0, len(descriptions))
	for i, desc := range descriptions {
		instances[i], err = d.apiInstance(&desc)
		if err != nil {
			d.cleanupFailedRunInstancesLaunch(ctx, ids)
			return nil, err
		}
		createdInstanceIDs = append(createdInstanceIDs, instances[i].InstanceID)
	}
	api.Logger(ctx).Info(
		"created instances",
		slog.Int("count", len(createdInstanceIDs)),
		slog.Any("instance_ids", createdInstanceIDs),
		slog.String("image_id", req.ImageID),
		slog.String("instance_type", req.InstanceType),
	)
	return &api.RunInstancesResponse{
		InstancesSet: instances,
	}, nil
}

func (d *Dispatcher) cleanupFailedRunInstancesLaunch(ctx context.Context, ids []executor.InstanceID) {
	if len(ids) == 0 {
		return
	}

	if _, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{InstanceIDs: ids}); err != nil {
		api.Logger(ctx).Warn("failed to terminate instances after run instances failure", slog.Any("error", err))
	}

	apiInstanceIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		apiID := apiInstanceID(id)
		apiInstanceIDs = append(apiInstanceIDs, apiID)
		if err := d.storage.RemoveResource(apiID); err != nil && !errors.As(err, &storage.ErrResourceNotFound{}) {
			api.Logger(ctx).Warn("failed to remove instance resource during rollback", slog.String("instance_id", apiID), slog.Any("error", err))
		}

		containerID := string(id)
		if err := d.imds.SetEnabled(containerID, true); err != nil {
			api.Logger(ctx).Warn("failed to reset IMDS endpoint during rollback", slog.String("container_id", containerID), slog.Any("error", err))
		}
		if err := d.imds.RevokeTokens(containerID); err != nil {
			api.Logger(ctx).Warn("failed to revoke IMDS tokens during rollback", slog.String("container_id", containerID), slog.Any("error", err))
		}
		if err := d.imds.SetTags(containerID, nil); err != nil {
			api.Logger(ctx).Warn("failed to clear IMDS tags during rollback", slog.String("container_id", containerID), slog.Any("error", err))
		}
	}
	if err := d.cleanupDeleteOnTerminationVolumesForInstances(ctx, apiInstanceIDs); err != nil {
		api.Logger(ctx).Warn("failed to clean delete-on-termination volumes during rollback", slog.Any("error", err))
	}
}

func (d *Dispatcher) applyRunInstancesDelay(ctx context.Context, hook testprofile.Hook, phase testprofile.Phase, req *api.RunInstancesRequest) error {
	if d.testProfile == nil {
		return nil
	}
	delay := d.testProfile.Delay(hook, phase, d.runInstancesMatchInput(req))
	if delay <= 0 {
		return nil
	}
	api.Logger(ctx).Debug(
		"applying run instances delay from test profile",
		slog.String("hook", string(hook)),
		slog.String("phase", string(phase)),
		slog.Duration("delay", delay),
		slog.String("instance_type", req.InstanceType),
	)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d *Dispatcher) runInstancesMatchInput(req *api.RunInstancesRequest) testprofile.MatchInput {
	out := testprofile.MatchInput{
		Action:       "RunInstances",
		InstanceType: req.InstanceType,
		MarketType:   "on-demand",
	}
	if req.InstanceMarketOptions != nil {
		if marketType := strings.TrimSpace(req.InstanceMarketOptions.MarketType); marketType != "" {
			out.MarketType = marketType
		}
	}
	if d.instanceTypeCatalog == nil {
		return out
	}
	data, ok := d.instanceTypeCatalog.InstanceTypes[req.InstanceType]
	if !ok {
		return out
	}
	if vcpu, ok := int64At(data, "VCpuInfo", "DefaultVCpus"); ok {
		out.VCPU = int(vcpu)
	}
	if memoryMiB, ok := int64At(data, "MemoryInfo", "SizeInMiB"); ok {
		out.MemoryMiB = int(memoryMiB)
	}
	return out
}

func (d *Dispatcher) dispatchDescribeInstances(ctx context.Context, req *api.DescribeInstancesRequest) (*api.DescribeInstancesResponse, error) {
	tagFilters, instanceFilters, err := splitInstanceFilters(req.Filters)
	if err != nil {
		return nil, err
	}
	instanceIDs, err := d.applyFilters(types.ResourceTypeInstance, req.InstanceIDs, tagFilters)
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

	instances := make([]api.Instance, 0, len(instanceIDs))
	describedInstances := make(map[string]struct{}, len(descriptions))
	for _, desc := range descriptions {
		instance, err := d.apiInstance(&desc)
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
		describedInstances[instance.InstanceID] = struct{}{}
	}
	for _, instanceID := range instanceIDs {
		if _, found := describedInstances[instanceID]; found {
			continue
		}
		terminatedInstance, include, err := d.terminatedInstanceFromStorage(instanceID)
		if err != nil {
			return nil, err
		}
		if include {
			instances = append(instances, terminatedInstance)
		}
	}
	instances, err = applyInstanceFilters(instances, instanceFilters)
	if err != nil {
		return nil, err
	}

	var reservations []api.Reservation
	if len(instances) > 0 {
		reservations = append(reservations, api.Reservation{InstancesSet: instances})
	}
	return &api.DescribeInstancesResponse{
		ReservationSet: reservations,
	}, nil
}

func (d *Dispatcher) dispatchDescribeInstanceStatus(ctx context.Context, req *api.DescribeInstanceStatusRequest) (*api.DescribeInstanceStatusResponse, error) {
	tagFilters, instanceFilters, err := splitInstanceFilters(req.Filters)
	if err != nil {
		return nil, err
	}
	instanceIDs, err := d.applyFilters(types.ResourceTypeInstance, req.InstanceIDs, tagFilters)
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

	includeAllInstances := req.IncludeAllInstances != nil && *req.IncludeAllInstances
	statuses := make([]api.InstanceStatus, 0, len(descriptions))
	for _, desc := range descriptions {
		if !includeAllInstances && desc.InstanceState.Name != api.InstanceStateRunning.Name {
			continue
		}
		instance, err := d.apiInstance(&desc)
		if err != nil {
			return nil, err
		}
		matches, err := instanceMatchesAllFilters(instance, instanceFilters)
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}

		instanceID := apiInstanceID(desc.InstanceID)
		attrs, err := d.storage.ResourceAttributes(instanceID)
		if err != nil {
			return nil, fmt.Errorf("retrieving instance attributes: %w", err)
		}
		availabilityZone, _ := attrs.Key(attributeNameAvailabilityZone)
		summary := statusSummaryForInstanceState(desc.InstanceState)
		statuses = append(statuses, api.InstanceStatus{
			AvailabilityZone: availabilityZone,
			InstanceID:       instanceID,
			InstanceState:    desc.InstanceState,
			InstanceStatus:   summary,
			SystemStatus:     summary,
		})
	}

	statuses, nextToken, err := applyNextToken(statuses, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, err
	}
	return &api.DescribeInstanceStatusResponse{
		InstanceStatusSet: statuses,
		NextToken:         nextToken,
	}, nil
}

func statusSummaryForInstanceState(state api.InstanceState) api.StatusSummary {
	summaryStatus := "not-applicable"
	detailStatus := "not-applicable"
	switch state.Name {
	case api.InstanceStateRunning.Name:
		summaryStatus = "ok"
		detailStatus = "passed"
	case api.InstanceStatePending.Name:
		summaryStatus = "initializing"
		detailStatus = "initializing"
	}
	return api.StatusSummary{
		Status: summaryStatus,
		Details: []api.StatusDetail{
			{
				Name:   "reachability",
				Status: detailStatus,
			},
		},
	}
}

func splitInstanceFilters(filters []api.Filter) ([]api.Filter, []api.Filter, error) {
	tagFilters := make([]api.Filter, 0, len(filters))
	instanceFilters := make([]api.Filter, 0, len(filters))
	for _, filter := range filters {
		if filter.Name == nil {
			return nil, nil, api.InvalidParameterValueError("Filter.Name", "<missing>")
		}
		if *filter.Name == "" {
			return nil, nil, api.InvalidParameterValueError("Filter.Name", "<empty>")
		}
		if filter.Values == nil {
			return nil, nil, api.InvalidParameterValueError("Filter.Values", "<missing>")
		}
		switch {
		case strings.HasPrefix(*filter.Name, "tag:"):
			tagFilters = append(tagFilters, filter)
		case *filter.Name == "tag-key":
			tagFilters = append(tagFilters, filter)
		case isSupportedInstanceFilter(*filter.Name):
			instanceFilters = append(instanceFilters, filter)
		default:
			return nil, nil, api.InvalidParameterValueError("Filter.Name", *filter.Name)
		}
	}
	return tagFilters, instanceFilters, nil
}

func isSupportedInstanceFilter(filterName string) bool {
	switch filterName {
	case "instance-id",
		"instance-state-name",
		"instance-type",
		"availability-zone",
		"private-ip-address",
		"ip-address",
		"private-dns-name",
		"dns-name":
		return true
	default:
		return false
	}
}

func applyInstanceFilters(instances []api.Instance, filters []api.Filter) ([]api.Instance, error) {
	filtered := instances
	for _, filter := range filters {
		next := make([]api.Instance, 0, len(filtered))
		for _, instance := range filtered {
			matches, err := instanceMatchesFilter(instance, filter)
			if err != nil {
				return nil, err
			}
			if matches {
				next = append(next, instance)
			}
		}
		filtered = next
	}
	return filtered, nil
}

func instanceMatchesAllFilters(instance api.Instance, filters []api.Filter) (bool, error) {
	for _, filter := range filters {
		matches, err := instanceMatchesFilter(instance, filter)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
	}
	return true, nil
}

func instanceMatchesFilter(instance api.Instance, filter api.Filter) (bool, error) {
	if filter.Name == nil {
		return false, api.InvalidParameterValueError("Filter.Name", "<missing>")
	}
	if filter.Values == nil {
		return false, api.InvalidParameterValueError("Filter.Values", "<missing>")
	}
	switch *filter.Name {
	case "instance-id":
		return slices.Contains(filter.Values, instance.InstanceID), nil
	case "instance-state-name":
		return slices.Contains(filter.Values, instance.InstanceState.Name), nil
	case "instance-type":
		return slices.Contains(filter.Values, instance.InstanceType), nil
	case "availability-zone":
		return slices.Contains(filter.Values, instance.Placement.AvailabilityZone), nil
	case "private-ip-address":
		return slices.Contains(filter.Values, instance.PrivateIPAddress), nil
	case "ip-address":
		return slices.Contains(filter.Values, instance.PublicIPAddress), nil
	case "private-dns-name":
		return slices.Contains(filter.Values, instance.PrivateDNSName), nil
	case "dns-name":
		return slices.Contains(filter.Values, instance.DNSName), nil
	default:
		return false, api.InvalidParameterValueError("Filter.Name", *filter.Name)
	}
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
	for _, change := range changes {
		instanceID := apiInstanceID(change.InstanceID)
		transitionTime := time.Now().UTC()
		if err := d.storage.SetResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameStateTransitionReason, Value: userInitiatedTransitionReason(transitionTime)},
		}); err != nil {
			return nil, fmt.Errorf("setting stop transition reason for %s: %w", instanceID, err)
		}
		if err := d.storage.RemoveResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameStateReasonCode},
			{Key: attributeNameStateReasonMessage},
			{Key: attributeNameInstanceTerminatedAt},
		}); err != nil {
			return nil, fmt.Errorf("clearing state reason for %s: %w", instanceID, err)
		}
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
	for _, change := range changes {
		instanceID := apiInstanceID(change.InstanceID)
		if err := d.storage.RemoveResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameStateTransitionReason},
			{Key: attributeNameStateReasonCode},
			{Key: attributeNameStateReasonMessage},
			{Key: attributeNameInstanceTerminatedAt},
		}); err != nil {
			return nil, fmt.Errorf("clearing transition metadata for %s: %w", instanceID, err)
		}
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
	if err := d.cleanupDeleteOnTerminationVolumesForInstances(ctx, req.InstanceIDs); err != nil {
		return nil, err
	}
	transitionTime := time.Now().UTC()
	for _, change := range changes {
		instanceID := apiInstanceID(change.InstanceID)
		if err := d.storage.SetResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameStateTransitionReason, Value: userInitiatedTransitionReason(transitionTime)},
			{Key: attributeNameStateReasonCode, Value: stateReasonUserInitiated},
			{Key: attributeNameStateReasonMessage, Value: stateMessageUserInitiated},
			{Key: attributeNameInstanceTerminatedAt, Value: transitionTime.Format(time.RFC3339Nano)},
		}); err != nil {
			return nil, fmt.Errorf("setting terminate transition reason for %s: %w", instanceID, err)
		}
	}
	for _, instanceID := range req.InstanceIDs {
		containerID := string(executorInstanceID(instanceID))
		if err := d.imds.SetEnabled(containerID, true); err != nil {
			return nil, fmt.Errorf("resetting IMDS endpoint for instance %s: %w", instanceID, err)
		}
		if err := d.imds.RevokeTokens(containerID); err != nil {
			return nil, fmt.Errorf("revoking IMDS tokens for instance %s: %w", instanceID, err)
		}
		if err := d.imds.SetTags(containerID, nil); err != nil {
			return nil, fmt.Errorf("clearing IMDS tags for instance %s: %w", instanceID, err)
		}
		api.Logger(ctx).Info("deleted instance", slog.String("instance_id", instanceID))
	}
	api.Logger(ctx).Info(
		"deleted instances",
		slog.Int("count", len(req.InstanceIDs)),
		slog.Any("instance_ids", req.InstanceIDs),
	)
	// TODO: remove resources from storage
	return &api.TerminateInstancesResponse{
		TerminatingInstances: apiInstanceChanges(changes),
	}, nil
}

func (d *Dispatcher) dispatchModifyInstanceMetadataOptions(ctx context.Context, req *api.ModifyInstanceMetadataOptionsRequest) (*api.ModifyInstanceMetadataOptionsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	if _, err := d.findInstance(ctx, req.InstanceID); err != nil {
		return nil, err
	}

	httpEndpoint := imdsEndpointEnabled
	if !d.imds.Enabled(string(executorInstanceID(req.InstanceID))) {
		httpEndpoint = imdsEndpointDisabled
	}
	if req.HTTPEndpoint != nil {
		switch strings.ToLower(*req.HTTPEndpoint) {
		case imdsEndpointEnabled:
			httpEndpoint = imdsEndpointEnabled
		case imdsEndpointDisabled:
			httpEndpoint = imdsEndpointDisabled
		default:
			return nil, api.InvalidParameterValueError("HttpEndpoint", *req.HTTPEndpoint)
		}
	}

	if err := d.imds.SetEnabled(string(executorInstanceID(req.InstanceID)), httpEndpoint == imdsEndpointEnabled); err != nil {
		return nil, fmt.Errorf("setting IMDS endpoint state for instance %s: %w", req.InstanceID, err)
	}
	instanceID := req.InstanceID
	return &api.ModifyInstanceMetadataOptionsResponse{
		InstanceID:              &instanceID,
		InstanceMetadataOptions: instanceMetadataOptions(httpEndpoint == imdsEndpointEnabled),
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
	stateTransitionReason, _ := attrs.Key(attributeNameStateTransitionReason)
	stateReason := stateReasonFromAttributes(attrs)
	privateDNSName := privateDNSNameFromIP(desc.PrivateIP, d.opts.Region, desc.PrivateDNSName)
	publicDNSName := publicDNSNameFromIP(desc.PublicIP, d.opts.Region, desc.PrivateDNSName)
	networkInterface := primaryNetworkInterface(
		instanceID,
		desc.PrivateIP,
		desc.PublicIP,
		privateDNSName,
		publicDNSName,
	)
	return api.Instance{
		InstanceID:            instanceID,
		ImageID:               desc.ImageID,
		InstanceState:         desc.InstanceState,
		StateTransitionReason: stateTransitionReason,
		StateReason:           stateReason,
		PrivateDNSName:        privateDNSName,
		DNSName:               publicDNSName,
		KeyName:               keyName,
		InstanceType:          desc.InstanceType,
		LaunchTime:            desc.LaunchTime,
		Architecture:          desc.Architecture,
		PrivateIPAddress:      desc.PrivateIP,
		PublicIPAddress:       desc.PublicIP,
		NetworkInterfaces: []api.InstanceNetworkInterface{
			networkInterface,
		},
		MetadataOptions: instanceMetadataOptions(d.imds.Enabled(string(desc.InstanceID))),
		TagSet:          tags,
		Placement: api.Placement{
			AvailabilityZone: availabilityZone,
		},
	}, nil
}

func instanceMetadataOptions(enabled bool) *api.InstanceMetadataOptions {
	httpEndpoint := imdsEndpointDisabled
	if enabled {
		httpEndpoint = imdsEndpointEnabled
	}
	return &api.InstanceMetadataOptions{
		HTTPEndpoint: &httpEndpoint,
		State:        new(imdsStateApplied),
	}
}

func stateReasonFromAttributes(attrs storage.Attributes) *api.StateReason {
	code, _ := attrs.Key(attributeNameStateReasonCode)
	message, _ := attrs.Key(attributeNameStateReasonMessage)
	if code == "" && message == "" {
		return nil
	}
	return &api.StateReason{
		Code:    code,
		Message: message,
	}
}

func (d *Dispatcher) terminatedInstanceFromStorage(instanceID string) (api.Instance, bool, error) {
	attrs, err := d.storage.ResourceAttributes(instanceID)
	if err != nil {
		var notFound storage.ErrResourceNotFound
		if errors.As(err, &notFound) {
			return api.Instance{}, false, nil
		}
		return api.Instance{}, false, fmt.Errorf("retrieving terminated instance attributes: %w", err)
	}
	terminatedAtRaw, ok := attrs.Key(attributeNameInstanceTerminatedAt)
	if !ok || terminatedAtRaw == "" {
		return api.Instance{}, false, nil
	}
	terminatedAt, err := parseTime(terminatedAtRaw)
	if err != nil {
		return api.Instance{}, false, fmt.Errorf("parsing terminated time for %s: %w", instanceID, err)
	}
	if time.Since(terminatedAt) > terminatedInstanceTTL {
		_ = d.storage.RemoveResource(instanceID)
		return api.Instance{}, false, nil
	}
	availabilityZone, _ := attrs.Key(attributeNameAvailabilityZone)
	stateTransitionReason, _ := attrs.Key(attributeNameStateTransitionReason)
	return api.Instance{
		InstanceID:            instanceID,
		InstanceState:         api.InstanceStateTerminated,
		StateTransitionReason: stateTransitionReason,
		StateReason:           stateReasonFromAttributes(attrs),
		LaunchTime:            terminatedAt,
		Placement:             api.Placement{AvailabilityZone: availabilityZone},
	}, true, nil
}

func userInitiatedTransitionReason(transitionTime time.Time) string {
	return fmt.Sprintf(
		"User initiated (%s GMT)",
		transitionTime.UTC().Format("2006-01-02 15:04:05"),
	)
}

func privateDNSNameFromIP(privateIP string, region string, fallback string) string {
	ipPart, ok := dashedIPv4ForDNS(privateIP)
	if !ok {
		return fallback
	}
	return fmt.Sprintf("ip-%s.%s.compute.internal", ipPart, region)
}

func publicDNSNameFromIP(publicIP string, region string, fallback string) string {
	ipPart, ok := dashedIPv4ForDNS(publicIP)
	if !ok {
		return fallback
	}
	return fmt.Sprintf("ec2-%s.%s.compute.internal", ipPart, region)
}

func dashedIPv4ForDNS(ip string) (string, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return "", false
	}
	return strings.ReplaceAll(addr.String(), ".", "-"), true
}

func primaryNetworkInterface(instanceID string, privateIP string, publicIP string, privateDNSName string, publicDNSName string) api.InstanceNetworkInterface {
	eniSuffix := strings.TrimPrefix(instanceID, instanceIDPrefix)
	if len(eniSuffix) > 17 {
		eniSuffix = eniSuffix[:17]
	}
	networkInterfaceID := "eni-" + eniSuffix
	attachmentID := "eni-attach-" + eniSuffix
	macAddress := syntheticMACAddress(instanceID)
	association := &api.InstanceNetworkInterfaceAssociation{
		PublicDNSName: publicDNSName,
		PublicIP:      publicIP,
		IPOwnerID:     "amazon",
	}
	if publicIP == "" {
		association = nil
	}

	return api.InstanceNetworkInterface{
		NetworkInterfaceID: networkInterfaceID,
		MacAddress:         macAddress,
		Status:             "in-use",
		SourceDestCheck:    true,
		PrivateDNSName:     privateDNSName,
		PrivateIPAddress:   privateIP,
		Association:        association,
		Attachment: &api.InstanceNetworkInterfaceAttachment{
			AttachmentID:        attachmentID,
			DeviceIndex:         0,
			Status:              "attached",
			DeleteOnTermination: true,
		},
		PrivateIPAddresses: []api.InstancePrivateIPAddressAssociation{
			{
				PrivateDNSName: privateDNSName,
				PrivateIP:      privateIP,
				Primary:        true,
				Association:    association,
			},
		},
	}
}

func syntheticMACAddress(instanceID string) string {
	sum := sha1.Sum([]byte(instanceID))
	// Set the locally administered bit and clear the multicast bit.
	sum[0] = (sum[0] | 0x02) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", sum[0], sum[1], sum[2], sum[3], sum[4], sum[5])
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

func normalizeUserData(raw string) string {
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	return raw
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
