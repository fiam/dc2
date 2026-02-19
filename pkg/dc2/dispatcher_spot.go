package dc2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	instanceMarketTypeOnDemand  = "on-demand"
	instanceMarketTypeSpot      = "spot"
	spotInstanceRequestIDPrefix = "sir-"

	spotInterruptionBehaviorTerminate = "terminate"
	spotInterruptionBehaviorStop      = "stop"
	spotInterruptionBehaviorHibernate = "hibernate"

	attributeNameInstanceMarketType = "InstanceMarketType"
	attributeNameSpotRequestID      = "SpotInstanceRequestID"
	attributeNameSpotMaxPrice       = "SpotMaxPrice"
	attributeNameSpotInterruptMode  = "SpotInterruptionBehavior"

	attributeNameSpotRequestState           = "SpotRequestState"
	attributeNameSpotRequestStatusCode      = "SpotRequestStatusCode"
	attributeNameSpotRequestStatusMessage   = "SpotRequestStatusMessage"
	attributeNameSpotRequestStatusUpdatedAt = "SpotRequestStatusUpdatedAt"
	attributeNameSpotRequestCreateTime      = "SpotRequestCreateTime"
	attributeNameSpotRequestInstanceID      = "SpotRequestInstanceID"
	attributeNameSpotRequestInstanceType    = "SpotRequestInstanceType"
	attributeNameSpotRequestType            = "SpotRequestType"
	attributeNameSpotRequestMaxPrice        = "SpotRequestMaxPrice"
	attributeNameSpotRequestInterruptMode   = "SpotRequestInterruptionBehavior"

	stateReasonSpotTerminationCode    = "Server.SpotInstanceTermination"
	stateReasonSpotTerminationMessage = "Server.SpotInstanceTermination: Instance terminated due to spot interruption"

	spotRequestTypeOneTime = "one-time"
	spotRequestStateActive = "active"
	spotRequestStateClosed = "closed"

	spotRequestStatusFulfilledCode         = "fulfilled"
	spotRequestStatusFulfilledMessage      = "Your spot request is fulfilled."
	spotRequestStatusByUserCode            = "instance-terminated-by-user"
	spotRequestStatusByUserMessage         = "Instance terminated by user."
	spotRequestStatusNoCapacityCode        = "instance-terminated-no-capacity"
	spotRequestStatusNoCapacityMessage     = "Instance terminated by simulated capacity interruption."
	spotRequestStatusServiceTerminatedCode = "instance-terminated-by-service"
	spotRequestStatusServiceTerminatedMsg  = "Instance terminated by service."
)

func normalizeMarketType(raw string) (string, error) {
	marketType := strings.ToLower(strings.TrimSpace(raw))
	switch marketType {
	case "", instanceMarketTypeOnDemand:
		return instanceMarketTypeOnDemand, nil
	case instanceMarketTypeSpot:
		return instanceMarketTypeSpot, nil
	default:
		return "", api.InvalidParameterValueError("InstanceMarketOptions.MarketType", raw)
	}
}

func normalizeSpotInterruptionBehavior(raw string) (string, error) {
	behavior := strings.ToLower(strings.TrimSpace(raw))
	switch behavior {
	case "", spotInterruptionBehaviorTerminate:
		return spotInterruptionBehaviorTerminate, nil
	case spotInterruptionBehaviorStop, spotInterruptionBehaviorHibernate:
		return behavior, nil
	default:
		return "", api.InvalidParameterValueError("InstanceMarketOptions.SpotOptions.InstanceInterruptionBehavior", raw)
	}
}

func normalizeSpotMaxPrice(raw string) (string, error) {
	price := strings.TrimSpace(raw)
	if price == "" {
		return "", nil
	}
	value, err := strconv.ParseFloat(price, 64)
	if err != nil || value <= 0 {
		return "", api.InvalidParameterValueError("InstanceMarketOptions.SpotOptions.MaxPrice", raw)
	}
	return price, nil
}

type spotLaunchOptions struct {
	MarketType           string
	MaxPrice             string
	InterruptionBehavior string
}

func resolveRunInstancesSpotOptions(req *api.RunInstancesRequest) (spotLaunchOptions, error) {
	out := spotLaunchOptions{
		MarketType:           instanceMarketTypeOnDemand,
		InterruptionBehavior: spotInterruptionBehaviorTerminate,
	}
	if req.InstanceMarketOptions == nil {
		return out, nil
	}

	marketType, err := normalizeMarketType(req.InstanceMarketOptions.MarketType)
	if err != nil {
		return out, err
	}
	spotOpts := req.InstanceMarketOptions.SpotOptions
	if strings.TrimSpace(req.InstanceMarketOptions.MarketType) == "" && spotOpts != nil {
		marketType = instanceMarketTypeSpot
	}
	out.MarketType = marketType

	if out.MarketType != instanceMarketTypeSpot {
		if spotOpts != nil && (strings.TrimSpace(spotOpts.MaxPrice) != "" || strings.TrimSpace(spotOpts.InstanceInterruptionBehavior) != "") {
			return out, api.InvalidParameterValueError("InstanceMarketOptions", "SpotOptions is only valid for MarketType=spot")
		}
		return out, nil
	}
	if spotOpts == nil {
		return out, nil
	}

	out.InterruptionBehavior, err = normalizeSpotInterruptionBehavior(spotOpts.InstanceInterruptionBehavior)
	if err != nil {
		return out, err
	}
	out.MaxPrice, err = normalizeSpotMaxPrice(spotOpts.MaxPrice)
	if err != nil {
		return out, err
	}
	return out, nil
}

func validateRunInstancesTagSpecifications(specs []api.TagSpecification, marketType string) error {
	for i, spec := range specs {
		switch spec.ResourceType {
		case types.ResourceTypeInstance:
		case types.ResourceTypeSpotInstancesRequest:
			if marketType != instanceMarketTypeSpot {
				return api.InvalidParameterValueError(
					fmt.Sprintf("TagSpecification.%d.ResourceType", i+1),
					string(spec.ResourceType),
				)
			}
		default:
			return api.InvalidParameterValueError(
				fmt.Sprintf("TagSpecification.%d.ResourceType", i+1),
				string(spec.ResourceType),
			)
		}
	}
	return nil
}

func splitRunInstancesTags(specs []api.TagSpecification) (map[string]string, map[string]string) {
	instanceTags := make(map[string]string)
	spotRequestTags := make(map[string]string)
	for _, spec := range specs {
		target := instanceTags
		if spec.ResourceType == types.ResourceTypeSpotInstancesRequest {
			target = spotRequestTags
		}
		for _, tag := range spec.Tags {
			target[tag.Key] = tag.Value
		}
	}
	return instanceTags, spotRequestTags
}

func (d *Dispatcher) registerSpotRequestForInstance(instanceID string, instanceType string, opts spotLaunchOptions, tags map[string]string) (string, error) {
	requestID, err := makeID(spotInstanceRequestIDPrefix)
	if err != nil {
		return "", err
	}
	if err := d.storage.RegisterResource(storage.Resource{Type: types.ResourceTypeSpotInstancesRequest, ID: requestID}); err != nil {
		return "", fmt.Errorf("registering spot request %s: %w", requestID, err)
	}
	now := time.Now().UTC()
	attrs := []storage.Attribute{
		{Key: attributeNameSpotRequestState, Value: spotRequestStateActive},
		{Key: attributeNameSpotRequestStatusCode, Value: spotRequestStatusFulfilledCode},
		{Key: attributeNameSpotRequestStatusMessage, Value: spotRequestStatusFulfilledMessage},
		{Key: attributeNameSpotRequestStatusUpdatedAt, Value: now.Format(time.RFC3339Nano)},
		{Key: attributeNameSpotRequestCreateTime, Value: now.Format(time.RFC3339Nano)},
		{Key: attributeNameSpotRequestInstanceID, Value: instanceID},
		{Key: attributeNameSpotRequestInstanceType, Value: instanceType},
		{Key: attributeNameSpotRequestType, Value: spotRequestTypeOneTime},
		{Key: attributeNameSpotRequestInterruptMode, Value: opts.InterruptionBehavior},
	}
	if opts.MaxPrice != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameSpotRequestMaxPrice, Value: opts.MaxPrice})
	}
	for key, value := range tags {
		attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(key), Value: value})
	}
	if err := d.storage.SetResourceAttributes(requestID, attrs); err != nil {
		return "", fmt.Errorf("storing spot request attributes for %s: %w", requestID, err)
	}
	return requestID, nil
}

func (d *Dispatcher) closeSpotRequestForInstance(instanceID string, code string, message string) error {
	attrs, err := d.storage.ResourceAttributes(instanceID)
	if err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil
		}
		return fmt.Errorf("retrieving instance attributes for %s: %w", instanceID, err)
	}
	spotRequestID, ok := attrs.Key(attributeNameSpotRequestID)
	if !ok || spotRequestID == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := d.storage.SetResourceAttributes(spotRequestID, []storage.Attribute{
		{Key: attributeNameSpotRequestState, Value: spotRequestStateClosed},
		{Key: attributeNameSpotRequestStatusCode, Value: code},
		{Key: attributeNameSpotRequestStatusMessage, Value: message},
		{Key: attributeNameSpotRequestStatusUpdatedAt, Value: now},
	}); err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil
		}
		return fmt.Errorf("setting spot request close state for %s: %w", spotRequestID, err)
	}
	return nil
}

func (d *Dispatcher) dispatchDescribeSpotInstanceRequests(_ context.Context, req *api.DescribeSpotInstanceRequestsRequest) (*api.DescribeSpotInstanceRequestsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	tagFilters, reqFilters, err := splitSpotRequestFilters(req.Filters)
	if err != nil {
		return nil, err
	}
	requestIDs, err := d.applyFilters(types.ResourceTypeSpotInstancesRequest, req.SpotInstanceRequestIDs, tagFilters)
	if err != nil {
		return nil, err
	}
	spotRequests := make([]api.SpotInstanceRequest, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		attrs, err := d.storage.ResourceAttributes(requestID)
		if err != nil {
			if errors.As(err, &storage.ErrResourceNotFound{}) {
				continue
			}
			return nil, fmt.Errorf("retrieving spot request attributes for %s: %w", requestID, err)
		}
		spotRequest, err := apiSpotRequestFromStorage(requestID, attrs)
		if err != nil {
			return nil, err
		}
		match, err := spotRequestMatchesFilters(spotRequest, reqFilters)
		if err != nil {
			return nil, err
		}
		if match {
			spotRequests = append(spotRequests, spotRequest)
		}
	}
	slices.SortFunc(spotRequests, func(a, b api.SpotInstanceRequest) int {
		return strings.Compare(a.SpotInstanceRequestID, b.SpotInstanceRequestID)
	})
	spotRequests, nextToken, err := applyNextToken(spotRequests, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, err
	}
	return &api.DescribeSpotInstanceRequestsResponse{
		SpotInstanceRequests: spotRequests,
		NextToken:            nextToken,
	}, nil
}

func splitSpotRequestFilters(filters []api.Filter) ([]api.Filter, []api.Filter, error) {
	tagFilters := make([]api.Filter, 0, len(filters))
	reqFilters := make([]api.Filter, 0, len(filters))
	for _, filter := range filters {
		if filter.Name == nil {
			return nil, nil, api.InvalidParameterValueError("Filter.Name", "")
		}
		name := strings.TrimSpace(*filter.Name)
		if name == "" {
			return nil, nil, api.InvalidParameterValueError("Filter.Name", "")
		}
		if name == "tag-key" || strings.HasPrefix(name, "tag:") {
			tagFilters = append(tagFilters, filter)
			continue
		}
		switch name {
		case "spot-instance-request-id", "state", "status-code", "status-message", "instance-id", "instance-type", "spot-price", "type":
			reqFilters = append(reqFilters, filter)
		default:
			return nil, nil, api.InvalidParameterValueError("Filter.Name", name)
		}
	}
	return tagFilters, reqFilters, nil
}

func apiSpotRequestFromStorage(requestID string, attrs storage.Attributes) (api.SpotInstanceRequest, error) {
	createTime, err := parseRequiredTimeAttr(attrs, attributeNameSpotRequestCreateTime, requestID)
	if err != nil {
		return api.SpotInstanceRequest{}, err
	}
	statusUpdatedAt, err := parseRequiredTimeAttr(attrs, attributeNameSpotRequestStatusUpdatedAt, requestID)
	if err != nil {
		return api.SpotInstanceRequest{}, err
	}
	state := attrOrDefault(attrs, attributeNameSpotRequestState, spotRequestStateActive)
	spotType := attrOrDefault(attrs, attributeNameSpotRequestType, spotRequestTypeOneTime)
	statusCode := attrOrDefault(attrs, attributeNameSpotRequestStatusCode, spotRequestStatusFulfilledCode)
	statusMessage := attrOrDefault(attrs, attributeNameSpotRequestStatusMessage, spotRequestStatusFulfilledMessage)
	instanceID, hasInstanceID := attrs.Key(attributeNameSpotRequestInstanceID)
	instanceType, _ := attrs.Key(attributeNameSpotRequestInstanceType)
	spotPrice, _ := attrs.Key(attributeNameSpotRequestMaxPrice)
	interruptMode, hasInterruptMode := attrs.Key(attributeNameSpotRequestInterruptMode)
	tags := tagsFromAttributes(attrs)

	var instanceIDPtr *string
	if hasInstanceID && instanceID != "" {
		id := instanceID
		instanceIDPtr = &id
	}
	var interruptModePtr *string
	if hasInterruptMode && interruptMode != "" {
		mode := interruptMode
		interruptModePtr = &mode
	}
	launchSpec := &api.SpotLaunchSpecification{InstanceType: instanceType}

	return api.SpotInstanceRequest{
		SpotInstanceRequestID:        requestID,
		State:                        state,
		SpotPrice:                    spotPrice,
		InstanceID:                   instanceIDPtr,
		CreateTime:                   createTime,
		Type:                         spotType,
		InstanceInterruptionBehavior: interruptModePtr,
		Status: api.SpotInstanceRequestStatus{
			Code:       statusCode,
			Message:    statusMessage,
			UpdateTime: statusUpdatedAt,
		},
		LaunchSpecification: launchSpec,
		TagSet:              tags,
	}, nil
}

func spotRequestMatchesFilters(req api.SpotInstanceRequest, filters []api.Filter) (bool, error) {
	for _, filter := range filters {
		matches, err := spotRequestMatchesFilter(req, filter)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
	}
	return true, nil
}

func spotRequestMatchesFilter(req api.SpotInstanceRequest, filter api.Filter) (bool, error) {
	if filter.Name == nil {
		return false, api.InvalidParameterValueError("Filter.Name", "")
	}
	switch *filter.Name {
	case "spot-instance-request-id":
		return slices.Contains(filter.Values, req.SpotInstanceRequestID), nil
	case "state":
		return slices.Contains(filter.Values, req.State), nil
	case "status-code":
		return slices.Contains(filter.Values, req.Status.Code), nil
	case "status-message":
		return slices.Contains(filter.Values, req.Status.Message), nil
	case "instance-id":
		if req.InstanceID == nil {
			return false, nil
		}
		return slices.Contains(filter.Values, *req.InstanceID), nil
	case "instance-type":
		if req.LaunchSpecification == nil {
			return false, nil
		}
		return slices.Contains(filter.Values, req.LaunchSpecification.InstanceType), nil
	case "spot-price":
		return slices.Contains(filter.Values, req.SpotPrice), nil
	case "type":
		return slices.Contains(filter.Values, req.Type), nil
	default:
		return false, api.InvalidParameterValueError("Filter.Name", *filter.Name)
	}
}

func parseRequiredTimeAttr(attrs storage.Attributes, key string, resourceID string) (time.Time, error) {
	raw, ok := attrs.Key(key)
	if !ok || raw == "" {
		return time.Time{}, fmt.Errorf("resource %s missing %s", resourceID, key)
	}
	parsed, err := parseTime(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing %s for resource %s: %w", key, resourceID, err)
	}
	return parsed, nil
}

func attrOrDefault(attrs storage.Attributes, key string, fallback string) string {
	value, ok := attrs.Key(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func tagsFromAttributes(attrs storage.Attributes) []api.Tag {
	tags := make([]api.Tag, 0)
	for _, attr := range attrs {
		if !attr.IsTag() {
			continue
		}
		tags = append(tags, api.Tag{Key: attr.TagKey(), Value: attr.Value})
	}
	slices.SortFunc(tags, func(a, b api.Tag) int {
		return strings.Compare(a.Key, b.Key)
	})
	return tags
}

type spotReclaimPlan struct {
	After  time.Duration
	Notice time.Duration
}

func (d *Dispatcher) resolveSpotReclaimPlan(req *api.RunInstancesRequest, marketType string) spotReclaimPlan {
	plan := spotReclaimPlan{
		After:  d.opts.SpotReclaimAfter,
		Notice: d.opts.SpotReclaimNotice,
	}
	if d.testProfile != nil {
		override := d.testProfile.SpotReclaim(d.runInstancesMatchInput(req))
		if override.After != nil {
			plan.After = *override.After
		}
		if override.Notice != nil {
			plan.Notice = *override.Notice
		}
	}
	if !strings.EqualFold(marketType, instanceMarketTypeSpot) {
		return spotReclaimPlan{}
	}
	if plan.After <= 0 {
		return spotReclaimPlan{}
	}
	if plan.Notice < 0 {
		plan.Notice = 0
	}
	if plan.Notice > plan.After {
		plan.Notice = plan.After
	}
	return plan
}

func (d *Dispatcher) scheduleSpotReclaim(instanceID string, plan spotReclaimPlan) {
	if plan.After <= 0 {
		return
	}

	reclaimAt := time.Now().UTC().Add(plan.After)
	notice := plan.Notice
	warnAt := reclaimAt.Add(-notice)
	runtimeID := string(executorInstanceID(instanceID))

	reclaimCtx, cancel := context.WithCancel(context.Background())
	d.spotReclaimMu.Lock()
	if existingCancel, found := d.spotReclaimCancels[instanceID]; found {
		existingCancel()
	}
	d.spotReclaimCancels[instanceID] = cancel
	d.spotReclaimMu.Unlock()

	go func() {
		defer d.cancelSpotReclaim(instanceID)
		defer func() {
			if err := d.imds.ClearSpotInstanceAction(runtimeID); err != nil {
				slog.Warn(
					"failed to clear spot interruption action",
					slog.String("instance_id", instanceID),
					slog.Any("error", err),
				)
			}
		}()

		if notice > 0 {
			if !waitUntil(reclaimCtx, warnAt) {
				return
			}
			if err := d.imds.SetSpotInstanceAction(runtimeID, "terminate", reclaimAt); err != nil {
				slog.Warn(
					"failed to set spot interruption action",
					slog.String("instance_id", instanceID),
					slog.Any("error", err),
				)
			}
		}

		if !waitUntil(reclaimCtx, reclaimAt) {
			return
		}
		if err := d.reclaimSpotInstance(instanceID, reclaimAt); err != nil {
			slog.Warn(
				"failed to reclaim spot instance",
				slog.String("instance_id", instanceID),
				slog.Any("error", err),
			)
		}
	}()
}

func (d *Dispatcher) cancelSpotReclaim(instanceID string) {
	d.spotReclaimMu.Lock()
	defer d.spotReclaimMu.Unlock()
	cancel, found := d.spotReclaimCancels[instanceID]
	if !found {
		return
	}
	delete(d.spotReclaimCancels, instanceID)
	cancel()
}

func (d *Dispatcher) cancelAllSpotReclaims() {
	d.spotReclaimMu.Lock()
	defer d.spotReclaimMu.Unlock()
	for instanceID, cancel := range d.spotReclaimCancels {
		cancel()
		delete(d.spotReclaimCancels, instanceID)
	}
}

func (d *Dispatcher) reclaimSpotInstance(instanceID string, reclaimAt time.Time) error {
	d.dispatchMu.Lock()
	defer d.dispatchMu.Unlock()

	ctx := context.Background()
	if _, err := d.findInstance(ctx, instanceID); err != nil {
		var invalidParamErr *api.Error
		if errors.As(err, &invalidParamErr) && invalidParamErr.Code == api.ErrorCodeInvalidParameterValue {
			return nil
		}
		return err
	}
	instanceAttrs, err := d.storage.ResourceAttributes(instanceID)
	if err != nil {
		return fmt.Errorf("retrieving instance attributes for reclaim %s: %w", instanceID, err)
	}
	behavior, _ := instanceAttrs.Key(attributeNameSpotInterruptMode)
	switch attrOrDefault(instanceAttrs, attributeNameSpotInterruptMode, spotInterruptionBehaviorTerminate) {
	case spotInterruptionBehaviorStop, spotInterruptionBehaviorHibernate:
		if err := d.stopInstancesForSpotReclaim(ctx, []string{instanceID}, behavior); err != nil {
			return fmt.Errorf("stopping reclaimed spot instance %s: %w", instanceID, err)
		}
	default:
		if _, err := d.terminateInstancesWithStateReason(
			ctx,
			[]string{instanceID},
			"Server.SpotInstanceTermination",
			stateReasonSpotTerminationCode,
			stateReasonSpotTerminationMessage,
			false,
		); err != nil {
			return fmt.Errorf("terminating reclaimed spot instance %s: %w", instanceID, err)
		}
	}
	slog.Info(
		"reclaimed spot instance",
		slog.String("instance_id", instanceID),
		slog.Time("termination_time", reclaimAt),
	)
	return nil
}

func (d *Dispatcher) stopInstancesForSpotReclaim(ctx context.Context, instanceIDs []string, behavior string) error {
	changes, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{
		InstanceIDs: executorInstanceIDs(instanceIDs),
		Force:       true,
	})
	if err != nil {
		return executorError(err)
	}
	transitionTime := time.Now().UTC()
	for _, change := range changes {
		instanceID := apiInstanceID(change.InstanceID)
		reason := fmt.Sprintf("Server.SpotInstanceInterruption:%s", behavior)
		if err := d.storage.SetResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameStateTransitionReason, Value: reason},
		}); err != nil {
			return fmt.Errorf("setting stop transition reason for %s: %w", instanceID, err)
		}
		if err := d.storage.RemoveResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameStateReasonCode},
			{Key: attributeNameStateReasonMessage},
			{Key: attributeNameInstanceTerminatedAt},
		}); err != nil {
			return fmt.Errorf("clearing state reason for %s: %w", instanceID, err)
		}
		if err := d.closeSpotRequestForInstance(instanceID, spotRequestStatusNoCapacityCode, spotRequestStatusNoCapacityMessage); err != nil {
			return err
		}
		slog.Info(
			"reclaimed spot instance with stop/hibernate behavior",
			slog.String("instance_id", instanceID),
			slog.String("behavior", behavior),
			slog.Time("reclaimed_at", transitionTime),
		)
	}
	return nil
}

func waitUntil(ctx context.Context, when time.Time) bool {
	delay := time.Until(when)
	if delay <= 0 {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
