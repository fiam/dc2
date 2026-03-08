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
	spotOptions, err := resolveRunInstancesSpotOptions(req)
	if err != nil {
		return nil, err
	}

	launchParams, err := d.resolveRunInstancesLaunchParameters(ctx, req)
	if err != nil {
		return nil, err
	}

	matchInput := d.runInstancesMatchInputForInstanceType(launchParams.instanceType)
	matchInput.MarketType = spotOptions.MarketType

	if err := validateRunInstancesTagSpecifications(req.TagSpecifications, spotOptions.MarketType); err != nil {
		return nil, err
	}
	if err := validateBlockDeviceMappings(launchParams.blockDeviceMappings, "BlockDeviceMapping"); err != nil {
		return nil, err
	}
	reclaimPlan := d.resolveSpotReclaimPlanForMatchInput(matchInput, spotOptions.MarketType)
	availabilityZone, err := d.runInstancesAvailabilityZone(req)
	if err != nil {
		return nil, err
	}
	subnetID := runInstancesSubnetID(req)
	vpcID := subnetVPCID(subnetID)
	if err := d.applyRunInstancesDelayForMatchInput(ctx, testprofile.HookBefore, testprofile.PhaseAllocate, matchInput); err != nil {
		return nil, err
	}
	ids, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      launchParams.imageID,
		InstanceType: launchParams.instanceType,
		Count:        req.MaxCount,
		UserData:     normalizeUserData(launchParams.userData),
	})
	if err != nil {
		return nil, executorError(err)
	}
	if err := d.applyRunInstancesDelayForMatchInput(ctx, testprofile.HookAfter, testprofile.PhaseAllocate, matchInput); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, err
	}

	attrs := []storage.Attribute{
		{
			Key: attributeNameAvailabilityZone, Value: availabilityZone,
		},
		{
			Key: attributeNameSubnetID, Value: subnetID,
		},
		{
			Key: attributeNameVPCID, Value: vpcID,
		},
	}
	if req.KeyName != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceKeyName, Value: req.KeyName})
	}
	if launchParams.userData != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceUserData, Value: normalizeUserData(launchParams.userData)})
	}
	if spotOptions.MarketType == instanceMarketTypeSpot {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceMarketType, Value: spotOptions.MarketType})
		attrs = append(attrs, storage.Attribute{Key: attributeNameSpotInterruptMode, Value: spotOptions.InterruptionBehavior})
		if spotOptions.MaxPrice != "" {
			attrs = append(attrs, storage.Attribute{Key: attributeNameSpotMaxPrice, Value: spotOptions.MaxPrice})
		}
	}
	instanceTags, spotRequestTags := splitRunInstancesTags(req.TagSpecifications)
	instanceTags = ensureLaunchTemplateLinkageTags(instanceTags, launchParams.launchTemplateID, launchParams.launchTemplateVersion)
	attrs = append(attrs, launchTemplateLinkageTagAttributes(launchParams.launchTemplateID, launchParams.launchTemplateVersion)...)
	for key, value := range instanceTags {
		attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(key), Value: value})
	}

	for _, executorID := range ids {
		id := string(instanceIDPrefix + executorID)
		instanceAttrs := append([]storage.Attribute{}, attrs...)
		if spotOptions.MarketType == instanceMarketTypeSpot {
			spotRequestID, err := d.registerSpotRequestForInstance(id, launchParams.instanceType, spotOptions, spotRequestTags)
			if err != nil {
				d.cleanupFailedRunInstancesLaunch(ctx, ids)
				return nil, err
			}
			instanceAttrs = append(instanceAttrs, storage.Attribute{Key: attributeNameSpotRequestID, Value: spotRequestID})
		}
		r := storage.Resource{Type: types.ResourceTypeInstance, ID: id}
		if err := d.storage.RegisterResource(r); err != nil {
			d.cleanupFailedRunInstancesLaunch(ctx, ids)
			return nil, fmt.Errorf("registering instance %s: %w", id, err)
		}
		if len(instanceAttrs) > 0 {
			if err := d.storage.SetResourceAttributes(id, instanceAttrs); err != nil {
				d.cleanupFailedRunInstancesLaunch(ctx, ids)
				return nil, fmt.Errorf("storing instance attributes: %w", err)
			}
		}
		if err := d.imds.SetTags(string(executorID), instanceTags); err != nil {
			d.cleanupFailedRunInstancesLaunch(ctx, ids)
			return nil, fmt.Errorf("synchronizing IMDS tags for instance %s: %w", id, err)
		}
	}

	if err := d.applyRunInstancesDelayForMatchInput(ctx, testprofile.HookBefore, testprofile.PhaseStart, matchInput); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, err
	}
	if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: ids,
	}); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, executorError(err)
	}
	if err := d.applyRunInstancesDelayForMatchInput(ctx, testprofile.HookAfter, testprofile.PhaseStart, matchInput); err != nil {
		d.cleanupFailedRunInstancesLaunch(ctx, ids)
		return nil, err
	}
	if err := d.attachInstanceBlockDeviceMappings(ctx, ids, availabilityZone, launchParams.blockDeviceMappings); err != nil {
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
		slog.String("image_id", launchParams.imageID),
		slog.String("instance_type", launchParams.instanceType),
		slog.String("market_type", spotOptions.MarketType),
		slog.String("spot_interruption_behavior", spotOptions.InterruptionBehavior),
		slog.String("spot_max_price", spotOptions.MaxPrice),
		slog.Duration("spot_reclaim_after", reclaimPlan.After),
		slog.Duration("spot_reclaim_notice", reclaimPlan.Notice),
	)
	if reclaimPlan.After > 0 {
		for _, instanceID := range createdInstanceIDs {
			d.scheduleSpotReclaim(instanceID, reclaimPlan)
		}
	}
	return &api.RunInstancesResponse{
		InstancesSet: instances,
	}, nil
}

type runInstancesLaunchParameters struct {
	imageID               string
	instanceType          string
	userData              string
	blockDeviceMappings   []api.RunInstancesBlockDeviceMapping
	launchTemplateID      string
	launchTemplateVersion string
}

func (d *Dispatcher) resolveRunInstancesLaunchParameters(
	ctx context.Context,
	req *api.RunInstancesRequest,
) (runInstancesLaunchParameters, error) {
	out := runInstancesLaunchParameters{
		imageID:             strings.TrimSpace(req.ImageID),
		instanceType:        strings.TrimSpace(req.InstanceType),
		userData:            req.UserData,
		blockDeviceMappings: cloneBlockDeviceMappings(req.BlockDeviceMappings),
	}

	if req.LaunchTemplate != nil {
		lt, err := d.findLaunchTemplate(ctx, req.LaunchTemplate)
		if err != nil {
			return runInstancesLaunchParameters{}, err
		}
		out.launchTemplateID = lt.ID
		out.launchTemplateVersion = lt.Version
		if out.imageID == "" {
			out.imageID = lt.ImageID
		}
		if out.instanceType == "" {
			out.instanceType = lt.InstanceType
		}
		if strings.TrimSpace(out.userData) == "" {
			out.userData = lt.UserData
		}
		if len(out.blockDeviceMappings) == 0 {
			out.blockDeviceMappings = cloneBlockDeviceMappings(lt.BlockDeviceMappings)
		}
	}

	if out.imageID == "" {
		return runInstancesLaunchParameters{}, api.InvalidParameterValueError("ImageId", "<empty>")
	}
	if out.instanceType == "" {
		return runInstancesLaunchParameters{}, api.InvalidParameterValueError("InstanceType", "<empty>")
	}
	return out, nil
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
		d.cancelSpotReclaim(apiID)
		attrs, err := d.storage.ResourceAttributes(apiID)
		if err == nil {
			if spotRequestID, ok := attrs.Key(attributeNameSpotRequestID); ok && spotRequestID != "" {
				if removeErr := d.storage.RemoveResource(spotRequestID); removeErr != nil && !errors.As(removeErr, &storage.ErrResourceNotFound{}) {
					api.Logger(ctx).Warn("failed to remove spot request resource during rollback", slog.String("spot_request_id", spotRequestID), slog.Any("error", removeErr))
				}
			}
		} else if !errors.As(err, &storage.ErrResourceNotFound{}) {
			api.Logger(ctx).Warn("failed to read instance attributes during rollback", slog.String("instance_id", apiID), slog.Any("error", err))
		}
		if err := d.storage.RemoveResource(apiID); err != nil && !errors.As(err, &storage.ErrResourceNotFound{}) {
			api.Logger(ctx).Warn("failed to remove instance resource during rollback", slog.String("instance_id", apiID), slog.Any("error", err))
		}

		containerID := string(id)
		if err := d.imds.ClearSpotInstanceAction(containerID); err != nil {
			api.Logger(ctx).Warn("failed to clear spot interruption action during rollback", slog.String("container_id", containerID), slog.Any("error", err))
		}
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

func (d *Dispatcher) applyRunInstancesDelayForMatchInput(
	ctx context.Context,
	hook testprofile.Hook,
	phase testprofile.Phase,
	matchInput testprofile.MatchInput,
) error {
	return d.applyTestProfileDelayForMatchInputsInternal(
		ctx,
		hook,
		phase,
		[]testprofile.MatchInput{matchInput},
		false,
	)
}

func (d *Dispatcher) applyTestProfileDelayForMatchInputs(
	ctx context.Context,
	hook testprofile.Hook,
	phase testprofile.Phase,
	matchInputs []testprofile.MatchInput,
) error {
	return d.applyTestProfileDelayForMatchInputsInternal(ctx, hook, phase, matchInputs, false)
}

func (d *Dispatcher) applyRunInstancesDelayForMatchInputAllowConcurrentDispatch(
	ctx context.Context,
	hook testprofile.Hook,
	phase testprofile.Phase,
	matchInput testprofile.MatchInput,
) error {
	return d.applyTestProfileDelayForMatchInputsInternal(
		ctx,
		hook,
		phase,
		[]testprofile.MatchInput{matchInput},
		true,
	)
}

func (d *Dispatcher) applyTestProfileDelayForMatchInputsInternal(
	ctx context.Context,
	hook testprofile.Hook,
	phase testprofile.Phase,
	matchInputs []testprofile.MatchInput,
	releaseDispatchLockWhileWaiting bool,
) error {
	if len(matchInputs) == 0 {
		return nil
	}

	started := time.Now()
	for {
		delay := d.testProfileDelayForMatchInputs(hook, phase, matchInputs)
		if delay <= 0 {
			return nil
		}
		elapsed := time.Since(started)
		remaining := delay - elapsed
		if remaining <= 0 {
			return nil
		}
		api.Logger(ctx).Debug(
			"applying test profile delay",
			slog.String("action", matchInputs[0].Action),
			slog.String("hook", string(hook)),
			slog.String("phase", string(phase)),
			slog.Duration("target_delay", delay),
			slog.Duration("elapsed", elapsed),
			slog.Duration("remaining", remaining),
			slog.Int("match_input_count", len(matchInputs)),
		)
		timer := time.NewTimer(remaining)
		if !releaseDispatchLockWhileWaiting {
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			case <-d.testProfileUpdateCh:
				timer.Stop()
			}
			continue
		}

		d.dispatchMu.Unlock()
		select {
		case <-ctx.Done():
			timer.Stop()
			d.dispatchMu.Lock()
			return ctx.Err()
		case <-timer.C:
		case <-d.testProfileUpdateCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		d.dispatchMu.Lock()
	}
}

func (d *Dispatcher) testProfileDelayForMatchInputs(
	hook testprofile.Hook,
	phase testprofile.Phase,
	matchInputs []testprofile.MatchInput,
) time.Duration {
	profile := d.activeTestProfile()
	if profile == nil {
		return 0
	}
	maxDelay := time.Duration(0)
	for _, matchInput := range matchInputs {
		delay := profile.Delay(hook, phase, matchInput)
		if delay > maxDelay {
			maxDelay = delay
		}
	}
	return maxDelay
}

func (d *Dispatcher) lifecycleMatchInputs(
	ctx context.Context,
	action string,
	instanceIDs []string,
) ([]testprofile.MatchInput, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: executorInstanceIDs(instanceIDs),
	})
	if err != nil {
		return nil, executorError(err)
	}

	descriptionsByID := make(map[string]executor.InstanceDescription, len(descriptions))
	for _, desc := range descriptions {
		descriptionsByID[apiInstanceID(desc.InstanceID)] = desc
	}

	matchInputs := make([]testprofile.MatchInput, 0, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		desc, ok := descriptionsByID[instanceID]
		if !ok {
			continue
		}

		matchInput := d.runInstancesMatchInputForInstanceType(desc.InstanceType)
		matchInput.Action = action

		attrs, attrErr := d.storage.ResourceAttributes(instanceID)
		if attrErr != nil {
			var notFound storage.ErrResourceNotFound
			if !errors.As(attrErr, &notFound) {
				return nil, fmt.Errorf("retrieving instance attributes for %s: %w", instanceID, attrErr)
			}
		} else {
			if marketType, _ := attrs.Key(attributeNameInstanceMarketType); marketType != "" {
				matchInput.MarketType = marketType
			}
			if autoScalingGroupName, _ := attrs.Key(attributeNameAutoScalingGroupName); autoScalingGroupName != "" {
				matchInput.AutoScalingGroupName = autoScalingGroupName
			}
		}
		matchInputs = append(matchInputs, matchInput)
	}
	return matchInputs, nil
}

func (d *Dispatcher) stopInstancesWithProfileDelay(
	ctx context.Context,
	instanceIDs []executor.InstanceID,
	force bool,
) ([]executor.InstanceStateChange, error) {
	matchInputs, err := d.lifecycleMatchInputs(ctx, testprofile.ActionStopInstances, apiInstanceIDs(instanceIDs))
	if err != nil {
		return nil, err
	}
	if err := d.applyTestProfileDelayForMatchInputs(ctx, testprofile.HookBefore, testprofile.PhaseStop, matchInputs); err != nil {
		return nil, err
	}
	changes, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{
		InstanceIDs: instanceIDs,
		Force:       force,
	})
	if err != nil {
		return nil, executorError(err)
	}
	if err := d.applyTestProfileDelayForMatchInputs(ctx, testprofile.HookAfter, testprofile.PhaseStop, matchInputs); err != nil {
		return nil, err
	}
	return changes, nil
}

func (d *Dispatcher) startInstancesWithProfileDelay(
	ctx context.Context,
	instanceIDs []executor.InstanceID,
) ([]executor.InstanceStateChange, error) {
	matchInputs, err := d.lifecycleMatchInputs(ctx, testprofile.ActionStartInstances, apiInstanceIDs(instanceIDs))
	if err != nil {
		return nil, err
	}
	if err := d.applyTestProfileDelayForMatchInputs(ctx, testprofile.HookBefore, testprofile.PhaseStart, matchInputs); err != nil {
		return nil, err
	}
	changes, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: instanceIDs,
	})
	if err != nil {
		return nil, executorError(err)
	}
	if err := d.applyTestProfileDelayForMatchInputs(ctx, testprofile.HookAfter, testprofile.PhaseStart, matchInputs); err != nil {
		return nil, err
	}
	return changes, nil
}

func (d *Dispatcher) terminateInstancesWithProfileDelay(
	ctx context.Context,
	instanceIDs []executor.InstanceID,
	force bool,
) ([]executor.InstanceStateChange, error) {
	matchInputs, err := d.lifecycleMatchInputs(ctx, testprofile.ActionTerminateInstances, apiInstanceIDs(instanceIDs))
	if err != nil {
		return nil, err
	}
	if err := d.applyTestProfileDelayForMatchInputs(ctx, testprofile.HookBefore, testprofile.PhaseTerminate, matchInputs); err != nil {
		return nil, err
	}
	changes, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{
		InstanceIDs: instanceIDs,
		Force:       force,
	})
	if err != nil {
		return nil, executorError(err)
	}
	if err := d.applyTestProfileDelayForMatchInputs(ctx, testprofile.HookAfter, testprofile.PhaseTerminate, matchInputs); err != nil {
		return nil, err
	}
	return changes, nil
}

func (d *Dispatcher) runInstancesMatchInput(req *api.RunInstancesRequest) testprofile.MatchInput {
	out := d.runInstancesMatchInputForInstanceType(req.InstanceType)
	if spotOpts, err := resolveRunInstancesSpotOptions(req); err == nil {
		out.MarketType = spotOpts.MarketType
	}
	return out
}

func (d *Dispatcher) runInstancesMatchInputForInstanceType(instanceType string) testprofile.MatchInput {
	return d.runInstancesMatchInputForAutoScalingGroup(instanceType, "")
}

func (d *Dispatcher) runInstancesMatchInputForAutoScalingGroup(instanceType string, autoScalingGroupName string) testprofile.MatchInput {
	out := testprofile.MatchInput{
		Action:               testprofile.ActionRunInstances,
		InstanceType:         instanceType,
		MarketType:           instanceMarketTypeOnDemand,
		AutoScalingGroupName: autoScalingGroupName,
	}
	if d.instanceTypeCatalog == nil {
		return out
	}
	data, ok := d.instanceTypeCatalog.InstanceTypes[instanceType]
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
		"instance-lifecycle",
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
	case "instance-lifecycle":
		if instance.InstanceLifecycle == nil {
			return false, nil
		}
		return slices.Contains(filter.Values, *instance.InstanceLifecycle), nil
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
	changes, err := d.stopInstancesWithProfileDelay(ctx, ids, req.Force)
	if err != nil {
		return nil, err
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
	changes, err := d.startInstancesWithProfileDelay(ctx, ids)
	if err != nil {
		return nil, err
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
	changes, err := d.terminateInstancesWithStateReason(
		ctx,
		req.InstanceIDs,
		"",
		stateReasonUserInitiated,
		stateMessageUserInitiated,
		req.Force,
		true,
	)
	if err != nil {
		return nil, err
	}
	// TODO: remove resources from storage
	return &api.TerminateInstancesResponse{
		TerminatingInstances: changes,
	}, nil
}

func (d *Dispatcher) terminateInstancesWithStateReason(
	ctx context.Context,
	instanceIDs []string,
	transitionReason string,
	stateReasonCode string,
	stateReasonMessage string,
	force bool,
	emitDeleteLogs bool,
) ([]api.InstanceStateChange, error) {
	ids := executorInstanceIDs(instanceIDs)
	changes, err := d.terminateInstancesWithProfileDelay(ctx, ids, force)
	if err != nil {
		return nil, err
	}
	if err := d.cleanupDeleteOnTerminationVolumesForInstances(ctx, instanceIDs); err != nil {
		return nil, err
	}
	transitionTime := time.Now().UTC()
	resolvedTransitionReason := transitionReason
	if resolvedTransitionReason == "" {
		resolvedTransitionReason = userInitiatedTransitionReason(transitionTime)
	}
	for _, change := range changes {
		instanceID := apiInstanceID(change.InstanceID)
		if err := d.storage.SetResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameStateTransitionReason, Value: resolvedTransitionReason},
			{Key: attributeNameStateReasonCode, Value: stateReasonCode},
			{Key: attributeNameStateReasonMessage, Value: stateReasonMessage},
			{Key: attributeNameInstanceTerminatedAt, Value: transitionTime.Format(time.RFC3339Nano)},
		}); err != nil {
			return nil, fmt.Errorf("setting terminate transition reason for %s: %w", instanceID, err)
		}
	}
	for _, instanceID := range instanceIDs {
		d.cancelSpotReclaim(instanceID)
		containerID := string(executorInstanceID(instanceID))
		if err := d.imds.ClearSpotInstanceAction(containerID); err != nil {
			return nil, fmt.Errorf("clearing spot interruption action for instance %s: %w", instanceID, err)
		}
		if err := d.imds.SetEnabled(containerID, true); err != nil {
			return nil, fmt.Errorf("resetting IMDS endpoint for instance %s: %w", instanceID, err)
		}
		if err := d.imds.RevokeTokens(containerID); err != nil {
			return nil, fmt.Errorf("revoking IMDS tokens for instance %s: %w", instanceID, err)
		}
		if err := d.imds.SetTags(containerID, nil); err != nil {
			return nil, fmt.Errorf("clearing IMDS tags for instance %s: %w", instanceID, err)
		}
		spotStatusCode := spotRequestStatusByUserCode
		spotStatusMessage := spotRequestStatusByUserMessage
		if stateReasonCode == stateReasonSpotTerminationCode {
			spotStatusCode = spotRequestStatusNoCapacityCode
			spotStatusMessage = spotRequestStatusNoCapacityMessage
		}
		if err := d.closeSpotRequestForInstance(instanceID, spotStatusCode, spotStatusMessage); err != nil {
			return nil, err
		}
		if emitDeleteLogs {
			api.Logger(ctx).Info("deleted instance", slog.String("instance_id", instanceID))
		}
	}
	if emitDeleteLogs {
		api.Logger(ctx).Info(
			"deleted instances",
			slog.Int("count", len(instanceIDs)),
			slog.Any("instance_ids", instanceIDs),
		)
	}
	return apiInstanceChanges(changes), nil
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
	subnetID, _ := attrs.Key(attributeNameSubnetID)
	if subnetID == "" {
		subnetID = defaultSubnetID
	}
	vpcID, _ := attrs.Key(attributeNameVPCID)
	if vpcID == "" {
		vpcID = subnetVPCID(subnetID)
	}
	stateTransitionReason, _ := attrs.Key(attributeNameStateTransitionReason)
	stateReason := stateReasonFromAttributes(attrs)
	var instanceLifecycle *string
	if marketType, _ := attrs.Key(attributeNameInstanceMarketType); strings.EqualFold(marketType, instanceMarketTypeSpot) {
		lifecycle := instanceMarketTypeSpot
		instanceLifecycle = &lifecycle
	}
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
		InstanceLifecycle:     instanceLifecycle,
		LaunchTime:            desc.LaunchTime,
		Architecture:          desc.Architecture,
		SubnetID:              subnetID,
		VPCID:                 vpcID,
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
	subnetID, _ := attrs.Key(attributeNameSubnetID)
	if subnetID == "" {
		subnetID = defaultSubnetID
	}
	vpcID, _ := attrs.Key(attributeNameVPCID)
	if vpcID == "" {
		vpcID = subnetVPCID(subnetID)
	}
	stateTransitionReason, _ := attrs.Key(attributeNameStateTransitionReason)
	var instanceLifecycle *string
	if marketType, _ := attrs.Key(attributeNameInstanceMarketType); strings.EqualFold(marketType, instanceMarketTypeSpot) {
		lifecycle := instanceMarketTypeSpot
		instanceLifecycle = &lifecycle
	}
	return api.Instance{
		InstanceID:            instanceID,
		InstanceState:         api.InstanceStateTerminated,
		StateTransitionReason: stateTransitionReason,
		StateReason:           stateReasonFromAttributes(attrs),
		InstanceLifecycle:     instanceLifecycle,
		LaunchTime:            terminatedAt,
		Placement:             api.Placement{AvailabilityZone: availabilityZone},
		SubnetID:              subnetID,
		VPCID:                 vpcID,
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

func apiInstanceIDs(instanceIDs []executor.InstanceID) []string {
	out := make([]string, len(instanceIDs))
	for i, instanceID := range instanceIDs {
		out[i] = apiInstanceID(instanceID)
	}
	return out
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

func (d *Dispatcher) runInstancesAvailabilityZone(req *api.RunInstancesRequest) (string, error) {
	if req.Placement == nil || req.Placement.AvailabilityZone == "" {
		return defaultAvailabilityZone(d.opts.Region), nil
	}
	if err := validateAvailabilityZone(req.Placement.AvailabilityZone, d.opts.Region); err != nil {
		return "", err
	}
	return req.Placement.AvailabilityZone, nil
}

func runInstancesSubnetID(req *api.RunInstancesRequest) string {
	if req == nil {
		return defaultSubnetID
	}
	subnetID := strings.TrimSpace(req.SubnetID)
	if subnetID == "" {
		return defaultSubnetID
	}
	return subnetID
}

func subnetVPCID(subnetID string) string {
	normalizedSubnetID := strings.TrimSpace(subnetID)
	if normalizedSubnetID == "" || normalizedSubnetID == defaultSubnetID {
		return defaultSubnetVPCID
	}
	hash := sha1.Sum([]byte(normalizedSubnetID))
	return "vpc-" + fmt.Sprintf("%x", hash)[:17]
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
