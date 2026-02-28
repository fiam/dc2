package dc2

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
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
	attributeNameAutoScalingGroupName                              = "AutoScalingGroupName"
	attributeNameAutoScalingGroupMinSize                           = "AutoScalingGroupMinSize"
	attributeNameAutoScalingGroupMaxSize                           = "AutoScalingGroupMaxSize"
	attributeNameAutoScalingGroupDesiredCapacity                   = "AutoScalingGroupDesiredCapacity"
	attributeNameAutoScalingGroupCreatedTime                       = "AutoScalingGroupCreatedTime"
	attributeNameAutoScalingGroupLaunchTemplateID                  = "AutoScalingGroupLaunchTemplateID"
	attributeNameAutoScalingGroupLaunchTemplateName                = "AutoScalingGroupLaunchTemplateName"
	attributeNameAutoScalingGroupLaunchTemplateVersion             = "AutoScalingGroupLaunchTemplateVersion"
	attributeNameAutoScalingGroupLaunchTemplateImageID             = "AutoScalingGroupLaunchTemplateImageID"
	attributeNameAutoScalingGroupLaunchTemplateType                = "AutoScalingGroupLaunchTemplateInstanceType"
	attributeNameAutoScalingGroupLaunchTemplateUserData            = "AutoScalingGroupLaunchTemplateUserData"
	attributeNameAutoScalingGroupLaunchTemplateBlockDeviceMappings = "AutoScalingGroupLaunchTemplateBlockDeviceMappings"
	attributeNameAutoScalingGroupVPCZoneIdentifier                 = "AutoScalingGroupVPCZoneIdentifier"
	attributeNameAutoScalingGroupDefaultCooldown                   = "AutoScalingGroupDefaultCooldown"
	attributeNameAutoScalingGroupHealthCheckType                   = "AutoScalingGroupHealthCheckType"
	attributeNameAutoScalingGroupInstanceType                      = "AutoScalingGroupInstanceType"
	attributeNameAutoScalingGroupWarmPoolEnabled                   = "AutoScalingGroupWarmPoolEnabled"
	attributeNameAutoScalingGroupWarmPoolMinSize                   = "AutoScalingGroupWarmPoolMinSize"
	attributeNameAutoScalingGroupWarmPoolMaxGroupPreparedCapacity  = "AutoScalingGroupWarmPoolMaxGroupPreparedCapacity"
	attributeNameAutoScalingGroupWarmPoolState                     = "AutoScalingGroupWarmPoolState"
	attributeNameAutoScalingGroupWarmPoolStatus                    = "AutoScalingGroupWarmPoolStatus"
	attributeNameAutoScalingGroupWarmPoolReuseOnScaleIn            = "AutoScalingGroupWarmPoolReuseOnScaleIn"
	attributeNameAutoScalingInstanceWarmPool                       = "AutoScalingInstanceWarmPool"
	autoScalingTagResourceType                                     = "auto-scaling-group"

	autoScalingDefaultCooldown = 300
	autoScalingHealthStatus    = "Healthy"
	autoScalingHealthCheckType = "EC2"
	autoScalingLifecycleState  = "InService"
	warmPoolStateStopped       = "Stopped"
	warmPoolStateRunning       = "Running"
	warmPoolStateHibernated    = "Hibernated"

	autoScalingWarmLifecycleStatePending    = "Warmed:Pending"
	autoScalingWarmLifecycleStateStopped    = "Warmed:Stopped"
	autoScalingWarmLifecycleStateRunning    = "Warmed:Running"
	autoScalingWarmLifecycleStateHibernated = "Warmed:Hibernated"
	warmPoolStatusActive                    = "Active"
	warmPoolStatusPendingDelete             = "PendingDelete"
	warmPoolAsyncDeleteInitialDelay         = 250 * time.Millisecond
	warmPoolAsyncDeleteMaxDelay             = 2 * time.Second
)

type autoScalingGroupData struct {
	Name                              string
	MinSize                           int
	MaxSize                           int
	DesiredCapacity                   int
	CreatedTime                       time.Time
	LaunchTemplateID                  string
	LaunchTemplateName                string
	LaunchTemplateVersion             string
	LaunchTemplateImageID             string
	LaunchTemplateInstanceType        string
	LaunchTemplateUserData            string
	LaunchTemplateBlockDeviceMappings []api.RunInstancesBlockDeviceMapping
	VPCZoneIdentifier                 *string
	DefaultCooldown                   int
	HealthCheckType                   string
	WarmPoolEnabled                   bool
	WarmPoolMinSize                   int
	WarmPoolMaxGroupPreparedCapacity  *int
	WarmPoolState                     string
	WarmPoolStatus                    string
	WarmPoolReuseOnScaleIn            *bool
}

func (d *Dispatcher) dispatchCreateOrUpdateAutoScalingTags(ctx context.Context, req *api.CreateOrUpdateAutoScalingTagsRequest) (*api.CreateOrUpdateTagsResponse, error) {
	if len(req.Tags) > tagRequestCountLimit {
		return nil, api.InvalidParameterValueError("Tags", fmt.Sprintf("length %d exceeds limit %d", len(req.Tags), tagRequestCountLimit))
	}

	attrsByResourceID := make(map[string][]storage.Attribute, len(req.Tags))
	for i, tag := range req.Tags {
		paramPrefix := fmt.Sprintf("Tags.member.%d", i+1)

		resourceID, attr, err := autoScalingTagAttribute(tag, paramPrefix, "", true)
		if err != nil {
			return nil, err
		}

		if _, err := d.findResource(ctx, types.ResourceTypeAutoScalingGroup, resourceID); err != nil {
			if errors.As(err, &storage.ErrResourceNotFound{}) {
				return nil, api.ErrWithCode("ValidationError", fmt.Errorf("auto scaling group %q was not found", resourceID))
			}
			return nil, fmt.Errorf("retrieving auto scaling group %q: %w", resourceID, err)
		}

		attrsByResourceID[resourceID] = append(attrsByResourceID[resourceID], attr)
	}

	for resourceID, attrs := range attrsByResourceID {
		if err := d.storage.SetResourceAttributes(resourceID, attrs); err != nil {
			return nil, fmt.Errorf("setting resource attributes for %s: %w", resourceID, err)
		}
	}

	return &api.CreateOrUpdateTagsResponse{}, nil
}

func (d *Dispatcher) dispatchCreateAutoScalingGroup(ctx context.Context, req *api.CreateAutoScalingGroupRequest) (*api.CreateAutoScalingGroupResponse, error) {
	if len(req.Tags) > tagRequestCountLimit {
		return nil, api.InvalidParameterValueError("Tags", fmt.Sprintf("length %d exceeds limit %d", len(req.Tags), tagRequestCountLimit))
	}

	lt, err := d.findLaunchTemplate(ctx, req.LaunchTemplate)
	if err != nil {
		return nil, err
	}
	if err := validateAutoScalingGroupSizes(req.MinSize, req.MaxSize); err != nil {
		return nil, err
	}
	desiredCapacity := req.MinSize
	if req.DesiredCapacity != nil {
		desiredCapacity = *req.DesiredCapacity
	}
	if err := validateDesiredCapacity(desiredCapacity, req.MinSize, req.MaxSize); err != nil {
		return nil, err
	}
	if lt.ImageID == "" || lt.InstanceType == "" {
		return nil, api.ErrWithCode("ValidationError", fmt.Errorf("launch template must define ImageId and InstanceType"))
	}

	if err := d.storage.RegisterResource(storage.Resource{Type: types.ResourceTypeAutoScalingGroup, ID: req.AutoScalingGroupName}); err != nil {
		if errors.As(err, &storage.ErrDuplicatedResource{}) {
			return nil, api.ErrWithCode("AlreadyExists", fmt.Errorf("auto scaling group %q already exists", req.AutoScalingGroupName))
		}
		return nil, fmt.Errorf("registering auto scaling group: %w", err)
	}

	group := autoScalingGroupData{
		Name:                              req.AutoScalingGroupName,
		MinSize:                           req.MinSize,
		MaxSize:                           req.MaxSize,
		DesiredCapacity:                   desiredCapacity,
		CreatedTime:                       time.Now().UTC(),
		LaunchTemplateID:                  lt.ID,
		LaunchTemplateName:                lt.Name,
		LaunchTemplateVersion:             lt.Version,
		LaunchTemplateImageID:             lt.ImageID,
		LaunchTemplateInstanceType:        lt.InstanceType,
		LaunchTemplateUserData:            lt.UserData,
		LaunchTemplateBlockDeviceMappings: cloneBlockDeviceMappings(lt.BlockDeviceMappings),
		VPCZoneIdentifier:                 req.VPCZoneIdentifier,
		DefaultCooldown:                   autoScalingDefaultCooldown,
		HealthCheckType:                   autoScalingHealthCheckType,
		WarmPoolState:                     warmPoolStateStopped,
	}
	if err := d.saveAutoScalingGroupData(&group); err != nil {
		_ = d.storage.RemoveResource(req.AutoScalingGroupName)
		return nil, err
	}

	if len(req.Tags) > 0 {
		attrs := make([]storage.Attribute, 0, len(req.Tags))
		for i, tag := range req.Tags {
			paramPrefix := fmt.Sprintf("Tags.member.%d", i+1)
			_, attr, err := autoScalingTagAttribute(tag, paramPrefix, req.AutoScalingGroupName, false)
			if err != nil {
				_ = d.storage.RemoveResource(req.AutoScalingGroupName)
				return nil, err
			}
			attrs = append(attrs, attr)
		}
		if err := d.storage.SetResourceAttributes(req.AutoScalingGroupName, attrs); err != nil {
			_ = d.storage.RemoveResource(req.AutoScalingGroupName)
			return nil, fmt.Errorf("setting resource attributes for %s: %w", req.AutoScalingGroupName, err)
		}
	}

	if err := d.scaleAutoScalingGroupTo(ctx, &group, desiredCapacity); err != nil {
		_ = d.storage.RemoveResource(req.AutoScalingGroupName)
		return nil, err
	}
	api.Logger(ctx).Info(
		"created auto scaling group",
		slog.String("auto_scaling_group_name", req.AutoScalingGroupName),
		slog.Int("desired_capacity", desiredCapacity),
		slog.Int("min_size", req.MinSize),
		slog.Int("max_size", req.MaxSize),
		slog.String("launch_template_id", group.LaunchTemplateID),
	)
	return &api.CreateAutoScalingGroupResponse{}, nil
}

func autoScalingTagAttribute(
	tag api.AutoScalingTag,
	paramPrefix string,
	defaultResourceID string,
	requireResourceID bool,
) (string, storage.Attribute, error) {
	if tag.Key == nil || *tag.Key == "" {
		return "", storage.Attribute{}, api.InvalidParameterValueError(paramPrefix+".Key", "<empty>")
	}

	resourceID := defaultResourceID
	if tag.ResourceID != nil && *tag.ResourceID != "" {
		resourceID = *tag.ResourceID
	}
	if resourceID == "" && requireResourceID {
		return "", storage.Attribute{}, api.InvalidParameterValueError(paramPrefix+".ResourceId", "<empty>")
	}
	if defaultResourceID != "" && resourceID != defaultResourceID {
		return "", storage.Attribute{}, api.InvalidParameterValueError(paramPrefix+".ResourceId", resourceID)
	}

	if tag.ResourceType != nil && *tag.ResourceType != autoScalingTagResourceType {
		return "", storage.Attribute{}, api.InvalidParameterValueError(paramPrefix+".ResourceType", *tag.ResourceType)
	}

	value := ""
	if tag.Value != nil {
		value = *tag.Value
	}

	return resourceID, storage.Attribute{
		Key:   storage.TagAttributeName(*tag.Key),
		Value: value,
	}, nil
}

func (d *Dispatcher) dispatchDescribeAutoScalingGroups(ctx context.Context, req *api.DescribeAutoScalingGroupsRequest) (*api.DescribeAutoScalingGroupsResponse, error) {
	resources, err := d.storage.RegisteredResources(types.ResourceTypeAutoScalingGroup)
	if err != nil {
		return nil, fmt.Errorf("retrieving auto scaling groups: %w", err)
	}

	resourceIDs := make(map[string]struct{}, len(resources))
	selectedIDs := make([]string, 0, len(resources))
	for _, r := range resources {
		resourceIDs[r.ID] = struct{}{}
		selectedIDs = append(selectedIDs, r.ID)
	}

	if len(req.AutoScalingGroupNames) > 0 {
		selectedIDs = selectedIDs[:0]
		selectedSet := make(map[string]struct{}, len(req.AutoScalingGroupNames))
		for _, name := range req.AutoScalingGroupNames {
			if _, ok := resourceIDs[name]; !ok {
				continue
			}
			if _, seen := selectedSet[name]; seen {
				continue
			}
			selectedSet[name] = struct{}{}
			selectedIDs = append(selectedIDs, name)
		}
	}

	if len(req.Filters) > 0 && (len(req.AutoScalingGroupNames) == 0 || len(selectedIDs) != 0) {
		selectedIDs, err = d.applyFilters(types.ResourceTypeAutoScalingGroup, selectedIDs, asGeneralFilters(req.Filters))
		if err != nil {
			return nil, err
		}
	}

	selectedSet := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		selectedSet[id] = struct{}{}
	}

	groups := make([]api.AutoScalingGroup, 0, len(selectedSet))
	includeInstances := req.IncludeInstances == nil || *req.IncludeInstances
	for _, r := range resources {
		if _, ok := selectedSet[r.ID]; !ok {
			continue
		}
		group, err := d.loadAutoScalingGroupData(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		apiGroup, err := d.apiAutoScalingGroup(ctx, group, includeInstances)
		if err != nil {
			return nil, err
		}
		groups = append(groups, apiGroup)
	}

	groups, nextToken, err := applyNextToken(groups, req.NextToken, req.MaxRecords)
	if err != nil {
		return nil, err
	}

	return &api.DescribeAutoScalingGroupsResponse{
		DescribeAutoScalingGroupsResult: api.DescribeAutoScalingGroupsResult{
			AutoScalingGroups: groups,
			NextToken:         nextToken,
		},
	}, nil
}

func (d *Dispatcher) reconcileAllAutoScalingGroups(ctx context.Context) error {
	resources, err := d.storage.RegisteredResources(types.ResourceTypeAutoScalingGroup)
	if err != nil {
		return fmt.Errorf("retrieving auto scaling groups for reconciliation: %w", err)
	}
	for _, resource := range resources {
		group, err := d.loadAutoScalingGroupData(ctx, resource.ID)
		if err != nil {
			return err
		}
		if err := d.reconcileAutoScalingGroup(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) reconcilePendingAutoScalingEvents(ctx context.Context) error {
	d.pendingInstanceMu.Lock()
	if len(d.pendingInstances) == 0 {
		d.pendingInstanceMu.Unlock()
		return nil
	}
	pendingInstanceIDs := make([]string, 0, len(d.pendingInstances))
	for instanceID := range d.pendingInstances {
		pendingInstanceIDs = append(pendingInstanceIDs, instanceID)
	}
	d.pendingInstances = make(map[string]struct{})
	d.pendingInstanceMu.Unlock()
	api.Logger(ctx).Info(
		"processing pending auto scaling lifecycle events",
		slog.Int("instance_count", len(pendingInstanceIDs)),
		slog.Any("instance_ids", pendingInstanceIDs),
	)

	groupsToReconcile := make(map[string]struct{})
	for _, instanceID := range pendingInstanceIDs {
		attrs, err := d.storage.ResourceAttributes(instanceID)
		if err != nil {
			if errors.As(err, &storage.ErrResourceNotFound{}) {
				continue
			}
			return fmt.Errorf("retrieving instance attributes for event-based reconciliation: %w", err)
		}
		groupName, _ := attrs.Key(attributeNameAutoScalingGroupName)
		if groupName == "" {
			continue
		}
		groupsToReconcile[groupName] = struct{}{}
	}

	for groupName := range groupsToReconcile {
		if _, err := d.findResource(ctx, types.ResourceTypeAutoScalingGroup, groupName); err != nil {
			if errors.As(err, &storage.ErrResourceNotFound{}) {
				continue
			}
			return err
		}
		group, err := d.loadAutoScalingGroupData(ctx, groupName)
		if err != nil {
			return err
		}
		api.Logger(ctx).Info(
			"reconciling auto scaling group from lifecycle events",
			slog.String("auto_scaling_group_name", groupName),
		)
		if err := d.reconcileAutoScalingGroup(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

func asGeneralFilters(filters []api.AutoScalingFilter) []api.Filter {
	out := make([]api.Filter, 0, len(filters))
	for _, filter := range filters {
		out = append(out, api.Filter(filter))
	}
	return out
}

func (d *Dispatcher) dispatchUpdateAutoScalingGroup(ctx context.Context, req *api.UpdateAutoScalingGroupRequest) (*api.UpdateAutoScalingGroupResponse, error) {
	group, err := d.loadAutoScalingGroupData(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	launchTemplateChanged := false

	if req.MinSize != nil {
		group.MinSize = *req.MinSize
	}
	if req.MaxSize != nil {
		group.MaxSize = *req.MaxSize
	}
	if err := validateAutoScalingGroupSizes(group.MinSize, group.MaxSize); err != nil {
		return nil, err
	}

	if req.DesiredCapacity != nil {
		group.DesiredCapacity = *req.DesiredCapacity
	} else {
		group.DesiredCapacity = min(max(group.DesiredCapacity, group.MinSize), group.MaxSize)
	}
	if err := validateDesiredCapacity(group.DesiredCapacity, group.MinSize, group.MaxSize); err != nil {
		return nil, err
	}

	if req.LaunchTemplate != nil {
		lt, err := d.findLaunchTemplate(ctx, req.LaunchTemplate)
		if err != nil {
			return nil, err
		}
		if lt.ImageID == "" || lt.InstanceType == "" {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("launch template must define ImageId and InstanceType"))
		}
		launchTemplateChanged = autoScalingGroupLaunchTemplateChanged(group, lt)
		group.LaunchTemplateID = lt.ID
		group.LaunchTemplateName = lt.Name
		group.LaunchTemplateVersion = lt.Version
		group.LaunchTemplateImageID = lt.ImageID
		group.LaunchTemplateInstanceType = lt.InstanceType
		group.LaunchTemplateUserData = lt.UserData
		group.LaunchTemplateBlockDeviceMappings = cloneBlockDeviceMappings(lt.BlockDeviceMappings)
	}
	if req.VPCZoneIdentifier != nil {
		group.VPCZoneIdentifier = req.VPCZoneIdentifier
	}

	if err := d.saveAutoScalingGroupData(group); err != nil {
		return nil, err
	}
	if launchTemplateChanged {
		if err := d.recycleWarmPoolInstancesForLaunchTemplateUpdate(ctx, group); err != nil {
			return nil, err
		}
	}
	if err := d.scaleAutoScalingGroupTo(ctx, group, group.DesiredCapacity); err != nil {
		return nil, err
	}
	return &api.UpdateAutoScalingGroupResponse{}, nil
}

func autoScalingGroupLaunchTemplateChanged(group *autoScalingGroupData, lt *launchTemplateData) bool {
	if group.LaunchTemplateID != lt.ID {
		return true
	}
	if group.LaunchTemplateName != lt.Name {
		return true
	}
	if group.LaunchTemplateVersion != lt.Version {
		return true
	}
	if group.LaunchTemplateImageID != lt.ImageID {
		return true
	}
	if group.LaunchTemplateInstanceType != lt.InstanceType {
		return true
	}
	if group.LaunchTemplateUserData != lt.UserData {
		return true
	}
	return !reflect.DeepEqual(group.LaunchTemplateBlockDeviceMappings, lt.BlockDeviceMappings)
}

func (d *Dispatcher) recycleWarmPoolInstancesForLaunchTemplateUpdate(ctx context.Context, group *autoScalingGroupData) error {
	if !group.WarmPoolEnabled {
		return nil
	}
	warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDs(ctx, group.Name)
	if err != nil {
		return err
	}
	if len(warmPoolInstanceIDs) == 0 {
		return nil
	}
	api.Logger(ctx).Info(
		"recycling warm pool instances after launch template update",
		slog.String("auto_scaling_group_name", group.Name),
		slog.Int("instance_count", len(warmPoolInstanceIDs)),
		slog.Any("instance_ids", warmPoolInstanceIDs),
	)
	return d.terminateAutoScalingInstancesWithReason(ctx, warmPoolInstanceIDs, "warm-pool-launch-template-update")
}

func (d *Dispatcher) dispatchSetDesiredCapacity(ctx context.Context, req *api.SetDesiredCapacityRequest) (*api.SetDesiredCapacityResponse, error) {
	group, err := d.loadAutoScalingGroupData(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	if err := validateDesiredCapacity(req.DesiredCapacity, group.MinSize, group.MaxSize); err != nil {
		return nil, err
	}
	if err := d.scaleAutoScalingGroupTo(ctx, group, req.DesiredCapacity); err != nil {
		return nil, err
	}
	return &api.SetDesiredCapacityResponse{}, nil
}

func (d *Dispatcher) dispatchDetachInstances(ctx context.Context, req *api.DetachInstancesRequest) (*api.DetachInstancesResponse, error) {
	group, err := d.loadAutoScalingGroupData(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}

	currentInstanceIDs, err := d.autoScalingGroupInstanceIDs(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	currentInstancesSet := make(map[string]struct{}, len(currentInstanceIDs))
	for _, instanceID := range currentInstanceIDs {
		currentInstancesSet[instanceID] = struct{}{}
	}

	detachedInstanceIDs := make([]string, 0, len(req.InstanceIDs))
	detachedInstanceSet := make(map[string]struct{}, len(req.InstanceIDs))
	for _, instanceID := range req.InstanceIDs {
		if _, seen := detachedInstanceSet[instanceID]; seen {
			continue
		}
		detachedInstanceSet[instanceID] = struct{}{}

		if _, found := currentInstancesSet[instanceID]; !found {
			return nil, api.ErrWithCode(
				"ValidationError",
				fmt.Errorf("instance %q does not belong to auto scaling group %q", instanceID, req.AutoScalingGroupName),
			)
		}
		detachedInstanceIDs = append(detachedInstanceIDs, instanceID)
	}

	targetDesiredCapacity := group.DesiredCapacity
	decrementDesiredCapacity := req.ShouldDecrementDesiredCapacity != nil && *req.ShouldDecrementDesiredCapacity
	if decrementDesiredCapacity {
		targetDesiredCapacity -= len(detachedInstanceIDs)
		if err := validateDesiredCapacity(targetDesiredCapacity, group.MinSize, group.MaxSize); err != nil {
			return nil, err
		}
	}

	for _, instanceID := range detachedInstanceIDs {
		if err := d.storage.RemoveResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameAutoScalingGroupName},
			{Key: attributeNameAutoScalingGroupInstanceType},
		}); err != nil {
			return nil, fmt.Errorf(
				"removing auto scaling attributes for detached instance %s: %w",
				instanceID,
				err,
			)
		}
		api.Logger(ctx).Info(
			"detached instance from auto scaling group",
			slog.String("auto_scaling_group_name", req.AutoScalingGroupName),
			slog.String("instance_id", instanceID),
			slog.Bool("decrement_desired_capacity", decrementDesiredCapacity),
			slog.Int("desired_capacity_before", group.DesiredCapacity),
			slog.Int("desired_capacity_after", targetDesiredCapacity),
		)
	}

	if err := d.scaleAutoScalingGroupTo(ctx, group, targetDesiredCapacity); err != nil {
		return nil, err
	}
	return &api.DetachInstancesResponse{}, nil
}

func (d *Dispatcher) dispatchDeleteAutoScalingGroup(ctx context.Context, req *api.DeleteAutoScalingGroupRequest) (*api.DeleteAutoScalingGroupResponse, error) {
	if _, err := d.findResource(ctx, types.ResourceTypeAutoScalingGroup, req.AutoScalingGroupName); err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("auto scaling group %q was not found", req.AutoScalingGroupName))
		}
		return nil, err
	}
	d.cancelWarmPoolDeleteJob(req.AutoScalingGroupName)

	instanceIDs, err := d.autoScalingGroupInstanceIDs(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDs(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	instanceIDs = append(instanceIDs, warmPoolInstanceIDs...)
	forceDelete := req.ForceDelete != nil && *req.ForceDelete
	if len(instanceIDs) > 0 && !forceDelete {
		return nil, api.ErrWithCode("ResourceInUse", fmt.Errorf("auto scaling group %q still has instances", req.AutoScalingGroupName))
	}
	if len(instanceIDs) > 0 {
		if err := d.terminateAutoScalingInstances(ctx, instanceIDs); err != nil {
			return nil, err
		}
	}
	if err := d.storage.RemoveResource(req.AutoScalingGroupName); err != nil {
		return nil, fmt.Errorf("removing auto scaling group: %w", err)
	}
	return &api.DeleteAutoScalingGroupResponse{}, nil
}

func (d *Dispatcher) dispatchPutWarmPool(ctx context.Context, req *api.PutWarmPoolRequest) (*api.PutWarmPoolResponse, error) {
	group, err := d.loadAutoScalingGroupData(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}

	poolStateUpdated := false
	if req.MinSize != nil {
		if *req.MinSize < 0 {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("MinSize must be >= 0"))
		}
		group.WarmPoolMinSize = *req.MinSize
	}
	if req.MaxGroupPreparedCapacity != nil {
		if *req.MaxGroupPreparedCapacity < -1 {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("MaxGroupPreparedCapacity must be >= -1"))
		}
		if *req.MaxGroupPreparedCapacity == -1 {
			group.WarmPoolMaxGroupPreparedCapacity = nil
		} else {
			value := *req.MaxGroupPreparedCapacity
			group.WarmPoolMaxGroupPreparedCapacity = &value
		}
	}
	if req.PoolState != nil {
		state, err := parseWarmPoolState(*req.PoolState)
		if err != nil {
			return nil, err
		}
		group.WarmPoolState = state
		poolStateUpdated = true
	}
	if req.InstanceReusePolicy != nil && req.InstanceReusePolicy.ReuseOnScaleIn != nil {
		reuseOnScaleIn := *req.InstanceReusePolicy.ReuseOnScaleIn
		group.WarmPoolReuseOnScaleIn = &reuseOnScaleIn
	}
	if group.WarmPoolState == "" {
		group.WarmPoolState = warmPoolStateStopped
	}
	group.WarmPoolEnabled = true
	group.WarmPoolStatus = warmPoolStatusActive

	if err := d.saveAutoScalingGroupData(group); err != nil {
		return nil, err
	}
	d.cancelWarmPoolDeleteJob(req.AutoScalingGroupName)
	if err := d.reconcileWarmPool(ctx, group); err != nil {
		return nil, err
	}
	if poolStateUpdated {
		warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDs(ctx, req.AutoScalingGroupName)
		if err != nil {
			return nil, err
		}
		if err := d.reconcileWarmPoolInstanceState(ctx, group, warmPoolInstanceIDs); err != nil {
			return nil, err
		}
	}
	return &api.PutWarmPoolResponse{}, nil
}

func (d *Dispatcher) dispatchDescribeWarmPool(ctx context.Context, req *api.DescribeWarmPoolRequest) (*api.DescribeWarmPoolResponse, error) {
	group, err := d.loadAutoScalingGroupData(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}

	warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDsReadOnly(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: executorInstanceIDs(warmPoolInstanceIDs),
	})
	if err != nil {
		return nil, executorError(err)
	}
	descriptionsByID := make(map[string]executor.InstanceDescription, len(descriptions))
	for _, desc := range descriptions {
		descriptionsByID[apiInstanceID(desc.InstanceID)] = desc
	}

	launchTemplateID := group.LaunchTemplateID
	launchTemplateName := group.LaunchTemplateName
	launchTemplateVersion := group.LaunchTemplateVersion
	protectedFromScaleIn := false
	instances := make([]api.AutoScalingInstance, 0, len(warmPoolInstanceIDs))
	for _, instanceID := range warmPoolInstanceIDs {
		desc, ok := descriptionsByID[instanceID]
		if !ok {
			continue
		}
		availabilityZone := defaultAvailabilityZone(d.opts.Region)
		if attrs, attrErr := d.storage.ResourceAttributes(instanceID); attrErr == nil {
			if v, ok := attrs.Key(attributeNameAvailabilityZone); ok && v != "" {
				availabilityZone = v
			}
		}
		instanceIDCopy := instanceID
		instanceType := group.LaunchTemplateInstanceType
		if desc.InstanceType != "" {
			instanceType = desc.InstanceType
		}
		healthStatus := autoScalingHealthStatus
		lifecycleState := autoScalingWarmPoolLifecycleState(desc.InstanceState.Name, group.WarmPoolState)
		instanceLaunchTemplateID := launchTemplateID
		instanceLaunchTemplateName := launchTemplateName
		instanceLaunchTemplateVersion := launchTemplateVersion

		instances = append(instances, api.AutoScalingInstance{
			AvailabilityZone: &availabilityZone,
			HealthStatus:     &healthStatus,
			InstanceID:       &instanceIDCopy,
			InstanceType:     &instanceType,
			LaunchTemplate: &api.AutoScalingLaunchTemplateSpecification{
				LaunchTemplateID:   &instanceLaunchTemplateID,
				LaunchTemplateName: &instanceLaunchTemplateName,
				Version:            &instanceLaunchTemplateVersion,
			},
			LifecycleState:       lifecycleState,
			ProtectedFromScaleIn: &protectedFromScaleIn,
		})
	}

	instances, nextToken, err := applyNextToken(instances, req.NextToken, req.MaxRecords)
	if err != nil {
		return nil, err
	}

	var warmPoolConfiguration *api.WarmPoolConfiguration
	if group.WarmPoolEnabled {
		minSize := group.WarmPoolMinSize
		poolState := group.WarmPoolState
		if poolState == "" {
			poolState = warmPoolStateStopped
		}
		warmPoolConfiguration = &api.WarmPoolConfiguration{
			MinSize:   &minSize,
			PoolState: &poolState,
		}
		status := group.WarmPoolStatus
		if status == "" {
			status = warmPoolStatusActive
		}
		warmPoolConfiguration.Status = &status
		if group.WarmPoolMaxGroupPreparedCapacity != nil {
			value := *group.WarmPoolMaxGroupPreparedCapacity
			warmPoolConfiguration.MaxGroupPreparedCapacity = &value
		}
		if group.WarmPoolReuseOnScaleIn != nil {
			reuseOnScaleIn := *group.WarmPoolReuseOnScaleIn
			warmPoolConfiguration.InstanceReusePolicy = &api.WarmPoolInstanceReusePolicy{
				ReuseOnScaleIn: &reuseOnScaleIn,
			}
		}
	}

	return &api.DescribeWarmPoolResponse{
		DescribeWarmPoolResult: api.DescribeWarmPoolResult{
			Instances:             instances,
			NextToken:             nextToken,
			WarmPoolConfiguration: warmPoolConfiguration,
		},
	}, nil
}

func (d *Dispatcher) dispatchDeleteWarmPool(ctx context.Context, req *api.DeleteWarmPoolRequest) (*api.DeleteWarmPoolResponse, error) {
	group, err := d.loadAutoScalingGroupData(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	if !group.WarmPoolEnabled {
		d.cancelWarmPoolDeleteJob(req.AutoScalingGroupName)
		return &api.DeleteWarmPoolResponse{}, nil
	}

	group.WarmPoolStatus = warmPoolStatusPendingDelete
	if err := d.saveAutoScalingGroupData(group); err != nil {
		return nil, err
	}
	forceDelete := req.ForceDelete != nil && *req.ForceDelete
	if !forceDelete {
		d.scheduleAsyncWarmPoolDeletion(req.AutoScalingGroupName)
		return &api.DeleteWarmPoolResponse{}, nil
	}
	d.cancelWarmPoolDeleteJob(req.AutoScalingGroupName)
	warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDs(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
	if len(warmPoolInstanceIDs) > 0 {
		if err := d.terminateAutoScalingInstancesWithReason(ctx, warmPoolInstanceIDs, "delete-warm-pool"); err != nil {
			return nil, err
		}
	}

	clearWarmPoolConfiguration(group)

	if err := d.saveAutoScalingGroupData(group); err != nil {
		return nil, err
	}
	return &api.DeleteWarmPoolResponse{}, nil
}

func (d *Dispatcher) scaleAutoScalingGroupTo(ctx context.Context, group *autoScalingGroupData, desiredCapacity int) error {
	if err := validateDesiredCapacity(desiredCapacity, group.MinSize, group.MaxSize); err != nil {
		return err
	}
	instanceIDs, err := d.autoScalingGroupInstanceIDs(ctx, group.Name)
	if err != nil {
		return err
	}
	currentCapacity := len(instanceIDs)

	switch {
	case currentCapacity < desiredCapacity:
		addCount := desiredCapacity - currentCapacity
		promotedInstanceIDs, err := d.promoteWarmPoolInstances(ctx, group, addCount)
		if err != nil {
			return err
		}
		addCount -= len(promotedInstanceIDs)
		if len(promotedInstanceIDs) > 0 {
			api.Logger(ctx).Info(
				"promoted warm pool instances into auto scaling group",
				slog.String("auto_scaling_group_name", group.Name),
				slog.Int("promoted_instances", len(promotedInstanceIDs)),
				slog.Any("instance_ids", promotedInstanceIDs),
			)
		}
		if addCount == 0 {
			break
		}
		api.Logger(ctx).Info(
			"scaling up auto scaling group",
			slog.String("auto_scaling_group_name", group.Name),
			slog.Int("current_capacity", currentCapacity),
			slog.Int("target_capacity", desiredCapacity),
			slog.Int("add_instances", addCount),
		)
		if err := d.scaleOutAutoScalingGroup(ctx, group, addCount); err != nil {
			return err
		}
	case currentCapacity > desiredCapacity:
		redundant := currentCapacity - desiredCapacity
		slices.Sort(instanceIDs)
		removedInstanceIDs := slices.Clone(instanceIDs[:redundant])
		reuseOnScaleIn := group.WarmPoolEnabled && group.WarmPoolReuseOnScaleIn != nil && *group.WarmPoolReuseOnScaleIn
		if reuseOnScaleIn {
			if err := d.moveAutoScalingInstancesToWarmPool(ctx, group, removedInstanceIDs); err != nil {
				return err
			}
		} else {
			api.Logger(ctx).Info(
				"scaling down auto scaling group",
				slog.String("auto_scaling_group_name", group.Name),
				slog.Int("current_capacity", currentCapacity),
				slog.Int("target_capacity", desiredCapacity),
				slog.Int("remove_instances", redundant),
				slog.Any("instance_ids", removedInstanceIDs),
			)
			if err := d.terminateAutoScalingInstancesWithReason(ctx, removedInstanceIDs, "scale-in"); err != nil {
				return err
			}
		}
	}

	group.DesiredCapacity = desiredCapacity
	if err := d.saveAutoScalingGroupData(group); err != nil {
		return err
	}
	if err := d.reconcileWarmPool(ctx, group); err != nil {
		return err
	}
	return nil
}

func (d *Dispatcher) reconcileAutoScalingGroup(ctx context.Context, group *autoScalingGroupData) error {
	return d.scaleAutoScalingGroupTo(ctx, group, group.DesiredCapacity)
}

func (d *Dispatcher) scaleOutAutoScalingGroup(ctx context.Context, group *autoScalingGroupData, count int) error {
	created, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      group.LaunchTemplateImageID,
		InstanceType: group.LaunchTemplateInstanceType,
		Count:        count,
		UserData:     normalizeUserData(group.LaunchTemplateUserData),
	})
	if err != nil {
		return executorError(err)
	}

	availabilityZone := defaultAvailabilityZone(d.opts.Region)
	for _, instanceID := range created {
		id := apiInstanceID(instanceID)
		if err := d.storage.RegisterResource(storage.Resource{Type: types.ResourceTypeInstance, ID: id}); err != nil {
			return fmt.Errorf("registering auto scaling instance %s: %w", id, err)
		}
		attrs := []storage.Attribute{
			{Key: attributeNameAvailabilityZone, Value: availabilityZone},
			{Key: attributeNameAutoScalingGroupName, Value: group.Name},
			{Key: attributeNameAutoScalingGroupInstanceType, Value: group.LaunchTemplateInstanceType},
		}
		if group.LaunchTemplateUserData != "" {
			attrs = append(attrs, storage.Attribute{
				Key:   attributeNameInstanceUserData,
				Value: normalizeUserData(group.LaunchTemplateUserData),
			})
		}
		if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
			return fmt.Errorf("setting auto scaling instance attributes: %w", err)
		}
	}

	if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{InstanceIDs: created}); err != nil {
		return executorError(err)
	}
	if err := d.attachInstanceBlockDeviceMappings(ctx, created, availabilityZone, group.LaunchTemplateBlockDeviceMappings); err != nil {
		return err
	}
	createdIDs := make([]string, 0, len(created))
	for _, id := range created {
		createdIDs = append(createdIDs, apiInstanceID(id))
	}
	api.Logger(ctx).Info(
		"scaled out auto scaling group",
		slog.String("auto_scaling_group_name", group.Name),
		slog.Int("added_instances", len(createdIDs)),
		slog.Any("instance_ids", createdIDs),
	)
	return nil
}

func (d *Dispatcher) promoteWarmPoolInstances(ctx context.Context, group *autoScalingGroupData, count int) ([]string, error) {
	if count <= 0 {
		return nil, nil
	}

	warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDs(ctx, group.Name)
	if err != nil {
		return nil, err
	}
	if len(warmPoolInstanceIDs) == 0 {
		return nil, nil
	}
	if len(warmPoolInstanceIDs) > count {
		warmPoolInstanceIDs = slices.Clone(warmPoolInstanceIDs[:count])
	}

	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: executorInstanceIDs(warmPoolInstanceIDs),
	})
	if err != nil {
		return nil, executorError(err)
	}
	descriptionsByID := make(map[string]executor.InstanceDescription, len(descriptions))
	for _, desc := range descriptions {
		descriptionsByID[apiInstanceID(desc.InstanceID)] = desc
	}

	promotedInstanceIDs := make([]string, 0, len(warmPoolInstanceIDs))
	missingInstanceIDs := make([]string, 0)
	instancesToStart := make([]executor.InstanceID, 0)
	for _, instanceID := range warmPoolInstanceIDs {
		desc, ok := descriptionsByID[instanceID]
		if !ok {
			missingInstanceIDs = append(missingInstanceIDs, instanceID)
			continue
		}
		promotedInstanceIDs = append(promotedInstanceIDs, instanceID)
		if desc.InstanceState.Name != api.InstanceStateRunning.Name {
			instancesToStart = append(instancesToStart, desc.InstanceID)
		}
	}
	if len(missingInstanceIDs) > 0 {
		if err := d.cleanupMissingAutoScalingInstances(ctx, missingInstanceIDs); err != nil {
			return nil, err
		}
	}
	if len(instancesToStart) > 0 {
		if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{InstanceIDs: instancesToStart}); err != nil {
			return nil, executorError(err)
		}
	}
	for _, instanceID := range promotedInstanceIDs {
		if err := d.storage.RemoveResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameAutoScalingInstanceWarmPool},
			{Key: attributeNameStateTransitionReason},
			{Key: attributeNameStateReasonCode},
			{Key: attributeNameStateReasonMessage},
			{Key: attributeNameInstanceTerminatedAt},
		}); err != nil {
			return nil, fmt.Errorf("promoting warm pool instance %s: %w", instanceID, err)
		}
	}

	return promotedInstanceIDs, nil
}

func (d *Dispatcher) moveAutoScalingInstancesToWarmPool(ctx context.Context, group *autoScalingGroupData, instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return nil
	}
	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: executorInstanceIDs(instanceIDs),
	})
	if err != nil {
		return executorError(err)
	}
	descriptionsByID := make(map[string]executor.InstanceDescription, len(descriptions))
	for _, desc := range descriptions {
		descriptionsByID[apiInstanceID(desc.InstanceID)] = desc
	}

	toStart := make([]executor.InstanceID, 0)
	toStop := make([]executor.InstanceID, 0)
	for _, instanceID := range instanceIDs {
		desc, ok := descriptionsByID[instanceID]
		if !ok {
			continue
		}
		switch group.WarmPoolState {
		case warmPoolStateRunning:
			if desc.InstanceState.Name != api.InstanceStateRunning.Name {
				toStart = append(toStart, desc.InstanceID)
			}
		case "", warmPoolStateStopped, warmPoolStateHibernated:
			if desc.InstanceState.Name == api.InstanceStateRunning.Name {
				toStop = append(toStop, desc.InstanceID)
			}
		default:
			return api.ErrWithCode("ValidationError", fmt.Errorf("unsupported PoolState %q", group.WarmPoolState))
		}
	}
	if len(toStart) > 0 {
		if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{InstanceIDs: toStart}); err != nil {
			return executorError(err)
		}
	}
	if len(toStop) > 0 {
		if _, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{InstanceIDs: toStop}); err != nil {
			return executorError(err)
		}
	}

	for _, instanceID := range instanceIDs {
		if err := d.storage.SetResourceAttributes(instanceID, []storage.Attribute{
			{Key: attributeNameAutoScalingInstanceWarmPool, Value: "true"},
		}); err != nil {
			return fmt.Errorf("marking instance %s as warm pool: %w", instanceID, err)
		}
	}
	api.Logger(ctx).Info(
		"moved auto scaling instances into warm pool",
		slog.String("auto_scaling_group_name", group.Name),
		slog.String("pool_state", group.WarmPoolState),
		slog.Int("instance_count", len(instanceIDs)),
		slog.Any("instance_ids", instanceIDs),
	)
	return nil
}

func (d *Dispatcher) reconcileWarmPoolInstanceState(ctx context.Context, group *autoScalingGroupData, instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return nil
	}

	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: executorInstanceIDs(instanceIDs),
	})
	if err != nil {
		return executorError(err)
	}
	descriptionsByID := make(map[string]executor.InstanceDescription, len(descriptions))
	for _, desc := range descriptions {
		descriptionsByID[apiInstanceID(desc.InstanceID)] = desc
	}

	toStart := make([]executor.InstanceID, 0)
	toStop := make([]executor.InstanceID, 0)
	for _, instanceID := range instanceIDs {
		desc, ok := descriptionsByID[instanceID]
		if !ok {
			continue
		}
		switch group.WarmPoolState {
		case warmPoolStateRunning:
			if desc.InstanceState.Name == api.InstanceStateStopped.Name {
				toStart = append(toStart, desc.InstanceID)
			}
		case "", warmPoolStateStopped, warmPoolStateHibernated:
			if desc.InstanceState.Name == api.InstanceStateRunning.Name {
				toStop = append(toStop, desc.InstanceID)
			}
		default:
			return api.ErrWithCode("ValidationError", fmt.Errorf("unsupported PoolState %q", group.WarmPoolState))
		}
	}

	if len(toStart) > 0 {
		if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{InstanceIDs: toStart}); err != nil {
			return executorError(err)
		}
	}
	if len(toStop) > 0 {
		if _, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{InstanceIDs: toStop}); err != nil {
			return executorError(err)
		}
	}
	return nil
}

func (d *Dispatcher) scheduleAsyncWarmPoolDeletion(autoScalingGroupName string) {
	jobCtx, cancel := context.WithCancel(context.Background())
	d.warmPoolDeleteMu.Lock()
	if existing, ok := d.warmPoolDeleteJobs[autoScalingGroupName]; ok {
		existing.Cancel()
	}
	d.warmPoolDeleteSeq++
	jobID := d.warmPoolDeleteSeq
	d.warmPoolDeleteJobs[autoScalingGroupName] = warmPoolDeleteJob{
		ID:     jobID,
		Cancel: cancel,
	}
	d.warmPoolDeleteMu.Unlock()

	go func() {
		defer d.finishWarmPoolDeleteJob(autoScalingGroupName, jobID)

		backoff := warmPoolAsyncDeleteInitialDelay
		timer := time.NewTimer(backoff)
		defer timer.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-timer.C:
			}

			shouldRetry := false
			d.dispatchMu.Lock()
			ctx := context.Background()
			group, err := d.loadAutoScalingGroupData(ctx, autoScalingGroupName)
			if err != nil {
				d.dispatchMu.Unlock()
				var apiErr *api.Error
				if errors.As(err, &apiErr) && apiErr.Code == "ValidationError" {
					return
				}
				shouldRetry = true
				slog.Warn(
					"failed to load auto scaling group for async warm pool deletion",
					slog.String("auto_scaling_group_name", autoScalingGroupName),
					slog.Any("error", err),
				)
			} else if !group.WarmPoolEnabled || group.WarmPoolStatus != warmPoolStatusPendingDelete {
				d.dispatchMu.Unlock()
				return
			} else {
				if err := d.reconcileWarmPool(ctx, group); err != nil {
					shouldRetry = true
					slog.Warn(
						"failed to reconcile async warm pool deletion",
						slog.String("auto_scaling_group_name", autoScalingGroupName),
						slog.Any("error", err),
					)
				}
				d.dispatchMu.Unlock()
			}

			if !shouldRetry {
				return
			}
			backoff = min(backoff*2, warmPoolAsyncDeleteMaxDelay)
			timer.Reset(backoff)
		}
	}()
}

func (d *Dispatcher) cancelWarmPoolDeleteJob(autoScalingGroupName string) {
	d.warmPoolDeleteMu.Lock()
	job, ok := d.warmPoolDeleteJobs[autoScalingGroupName]
	if ok {
		delete(d.warmPoolDeleteJobs, autoScalingGroupName)
	}
	d.warmPoolDeleteMu.Unlock()
	if ok {
		job.Cancel()
	}
}

func (d *Dispatcher) finishWarmPoolDeleteJob(autoScalingGroupName string, jobID uint64) {
	d.warmPoolDeleteMu.Lock()
	defer d.warmPoolDeleteMu.Unlock()
	job, ok := d.warmPoolDeleteJobs[autoScalingGroupName]
	if !ok {
		return
	}
	if job.ID == jobID {
		delete(d.warmPoolDeleteJobs, autoScalingGroupName)
	}
}

func (d *Dispatcher) cancelAllWarmPoolDeleteJobs() {
	d.warmPoolDeleteMu.Lock()
	jobs := make([]warmPoolDeleteJob, 0, len(d.warmPoolDeleteJobs))
	for name, job := range d.warmPoolDeleteJobs {
		jobs = append(jobs, job)
		delete(d.warmPoolDeleteJobs, name)
	}
	d.warmPoolDeleteMu.Unlock()
	for _, job := range jobs {
		job.Cancel()
	}
}

func clearWarmPoolConfiguration(group *autoScalingGroupData) {
	group.WarmPoolEnabled = false
	group.WarmPoolMinSize = 0
	group.WarmPoolMaxGroupPreparedCapacity = nil
	group.WarmPoolState = ""
	group.WarmPoolStatus = ""
	group.WarmPoolReuseOnScaleIn = nil
}

func (d *Dispatcher) reconcileWarmPool(ctx context.Context, group *autoScalingGroupData) error {
	if !group.WarmPoolEnabled {
		return nil
	}

	warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDs(ctx, group.Name)
	if err != nil {
		return err
	}
	if group.WarmPoolStatus == warmPoolStatusPendingDelete {
		if len(warmPoolInstanceIDs) > 0 {
			if err := d.terminateAutoScalingInstancesWithReason(ctx, warmPoolInstanceIDs, "delete-warm-pool-pending"); err != nil {
				return err
			}
		}
		clearWarmPoolConfiguration(group)
		return d.saveAutoScalingGroupData(group)
	}

	targetCapacity := autoScalingWarmPoolTargetCapacity(group)
	currentCapacity := len(warmPoolInstanceIDs)
	switch {
	case currentCapacity < targetCapacity:
		addCount := targetCapacity - currentCapacity
		if err := d.scaleOutWarmPool(ctx, group, addCount); err != nil {
			return err
		}
	case currentCapacity > targetCapacity:
		redundant := currentCapacity - targetCapacity
		slices.Sort(warmPoolInstanceIDs)
		terminatedInstanceIDs := slices.Clone(warmPoolInstanceIDs[:redundant])
		if err := d.terminateAutoScalingInstancesWithReason(ctx, terminatedInstanceIDs, "warm-pool-scale-in"); err != nil {
			return err
		}
	}
	return nil
}

func autoScalingWarmPoolTargetCapacity(group *autoScalingGroupData) int {
	maxPreparedCapacity := group.MaxSize
	if group.WarmPoolMaxGroupPreparedCapacity != nil {
		maxPreparedCapacity = *group.WarmPoolMaxGroupPreparedCapacity
	}
	target := max(0, maxPreparedCapacity-group.DesiredCapacity)
	return max(target, group.WarmPoolMinSize)
}

func (d *Dispatcher) scaleOutWarmPool(ctx context.Context, group *autoScalingGroupData, count int) error {
	if count <= 0 {
		return nil
	}
	created, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      group.LaunchTemplateImageID,
		InstanceType: group.LaunchTemplateInstanceType,
		Count:        count,
		UserData:     normalizeUserData(group.LaunchTemplateUserData),
	})
	if err != nil {
		return executorError(err)
	}

	availabilityZone := defaultAvailabilityZone(d.opts.Region)
	for _, instanceID := range created {
		id := apiInstanceID(instanceID)
		if err := d.storage.RegisterResource(storage.Resource{Type: types.ResourceTypeInstance, ID: id}); err != nil {
			return fmt.Errorf("registering warm pool instance %s: %w", id, err)
		}
		attrs := []storage.Attribute{
			{Key: attributeNameAvailabilityZone, Value: availabilityZone},
			{Key: attributeNameAutoScalingGroupName, Value: group.Name},
			{Key: attributeNameAutoScalingGroupInstanceType, Value: group.LaunchTemplateInstanceType},
			{Key: attributeNameAutoScalingInstanceWarmPool, Value: "true"},
		}
		if group.LaunchTemplateUserData != "" {
			attrs = append(attrs, storage.Attribute{
				Key:   attributeNameInstanceUserData,
				Value: normalizeUserData(group.LaunchTemplateUserData),
			})
		}
		if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
			return fmt.Errorf("setting warm pool instance attributes: %w", err)
		}
	}

	if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{InstanceIDs: created}); err != nil {
		return executorError(err)
	}
	if err := d.attachInstanceBlockDeviceMappings(ctx, created, availabilityZone, group.LaunchTemplateBlockDeviceMappings); err != nil {
		return err
	}

	switch group.WarmPoolState {
	case "", warmPoolStateStopped, warmPoolStateHibernated:
		if _, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{InstanceIDs: created}); err != nil {
			return executorError(err)
		}
	case warmPoolStateRunning:
		// Keep instances running in the warm pool.
	default:
		return api.ErrWithCode("ValidationError", fmt.Errorf("unsupported PoolState %q", group.WarmPoolState))
	}

	createdIDs := make([]string, 0, len(created))
	for _, id := range created {
		createdIDs = append(createdIDs, apiInstanceID(id))
	}
	api.Logger(ctx).Info(
		"scaled out warm pool",
		slog.String("auto_scaling_group_name", group.Name),
		slog.Int("added_instances", len(createdIDs)),
		slog.String("pool_state", group.WarmPoolState),
		slog.Any("instance_ids", createdIDs),
	)
	return nil
}

func (d *Dispatcher) terminateAutoScalingInstances(ctx context.Context, instanceIDs []string) error {
	return d.terminateAutoScalingInstancesWithReason(ctx, instanceIDs, "")
}

func (d *Dispatcher) terminateAutoScalingInstancesWithReason(ctx context.Context, instanceIDs []string, reason string) error {
	if len(instanceIDs) == 0 {
		return nil
	}
	attrs := []any{
		slog.Int("count", len(instanceIDs)),
		slog.Any("instance_ids", instanceIDs),
	}
	if reason != "" {
		attrs = append(attrs, slog.String("reason", reason))
	}
	api.Logger(ctx).Info("terminating auto scaling instances", attrs...)
	for _, instanceID := range instanceIDs {
		if _, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{
			InstanceIDs: []executor.InstanceID{executorInstanceID(instanceID)},
		}); err != nil {
			var apiErr *api.Error
			if !errors.As(err, &apiErr) || apiErr.Code != api.ErrorCodeInstanceNotFound {
				return executorError(err)
			}
		}
	}
	if err := d.cleanupDeleteOnTerminationVolumesForInstances(ctx, instanceIDs); err != nil {
		return err
	}
	for _, instanceID := range instanceIDs {
		d.cancelSpotReclaim(instanceID)
		if err := d.imds.ClearSpotInstanceAction(string(executorInstanceID(instanceID))); err != nil {
			api.Logger(ctx).Warn("failed to clear spot interruption action while terminating auto scaling instance", "instance_id", instanceID, "error", err)
		}
		if err := d.closeSpotRequestForInstance(instanceID, spotRequestStatusServiceTerminatedCode, spotRequestStatusServiceTerminatedMsg); err != nil {
			api.Logger(ctx).Warn("failed to close spot request while terminating auto scaling instance", "instance_id", instanceID, "error", err)
		}
		if err := d.storage.RemoveResource(instanceID); err != nil && !errors.As(err, &storage.ErrResourceNotFound{}) {
			return fmt.Errorf("removing auto scaling instance %s: %w", instanceID, err)
		}
		d.cleanupAutoScalingInstanceMetadata(ctx, instanceID)
		attrs := []any{slog.String("instance_id", instanceID)}
		if reason != "" {
			attrs = append(attrs, slog.String("reason", reason))
		}
		api.Logger(ctx).Info("deleted auto scaling instance", attrs...)
	}
	return nil
}

func (d *Dispatcher) autoScalingGroupInstanceIDs(ctx context.Context, autoScalingGroupName string) ([]string, error) {
	return d.autoScalingGroupInstanceIDsForMode(ctx, autoScalingGroupName, true)
}

func (d *Dispatcher) autoScalingGroupInstanceIDsReadOnly(ctx context.Context, autoScalingGroupName string) ([]string, error) {
	return d.autoScalingGroupInstanceIDsForMode(ctx, autoScalingGroupName, false)
}

func (d *Dispatcher) autoScalingGroupInstanceIDsForMode(ctx context.Context, autoScalingGroupName string, reconcile bool) ([]string, error) {
	instances, err := d.storage.RegisteredResources(types.ResourceTypeInstance)
	if err != nil {
		return nil, fmt.Errorf("retrieving registered instances: %w", err)
	}
	instanceIDs := make([]string, 0, len(instances))
	for _, instance := range instances {
		attrs, err := d.storage.ResourceAttributes(instance.ID)
		if err != nil {
			return nil, fmt.Errorf("retrieving instance attributes: %w", err)
		}
		groupName, _ := attrs.Key(attributeNameAutoScalingGroupName)
		if groupName == autoScalingGroupName && !autoScalingInstanceIsWarm(attrs) {
			instanceIDs = append(instanceIDs, instance.ID)
		}
	}
	slices.Sort(instanceIDs)
	if len(instanceIDs) == 0 {
		return instanceIDs, nil
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

	liveIDs := make([]string, 0, len(instanceIDs))
	missingIDs := make([]string, 0)
	replaceIDs := make([]string, 0)
	replaceReasons := make([]string, 0)
	for _, instanceID := range instanceIDs {
		desc, ok := descriptionsByID[instanceID]
		if !ok {
			missingIDs = append(missingIDs, instanceID)
			continue
		}
		if autoScalingInstanceNeedsReplacement(desc) {
			if !reconcile {
				liveIDs = append(liveIDs, instanceID)
				continue
			}
			replaceIDs = append(replaceIDs, instanceID)
			replaceReasons = append(replaceReasons, fmt.Sprintf("%s:%s", instanceID, autoScalingInstanceReplacementReason(desc)))
			continue
		}
		liveIDs = append(liveIDs, instanceID)
	}
	if !reconcile {
		return liveIDs, nil
	}

	if len(replaceIDs) > 0 {
		api.Logger(ctx).Info(
			"replacing unhealthy or stopped auto scaling instances",
			slog.String("auto_scaling_group_name", autoScalingGroupName),
			slog.Any("replacements", replaceReasons),
		)
		if err := d.terminateAutoScalingInstancesWithReason(ctx, replaceIDs, "replacement:"+strings.Join(replaceReasons, ",")); err != nil {
			return nil, err
		}
	}
	if len(missingIDs) == 0 {
		return liveIDs, nil
	}
	api.Logger(ctx).Info(
		"reconciling missing auto scaling instances",
		slog.String("auto_scaling_group_name", autoScalingGroupName),
		slog.Int("missing_count", len(missingIDs)),
		slog.Any("missing_instance_ids", missingIDs),
	)
	if err := d.cleanupMissingAutoScalingInstances(ctx, missingIDs); err != nil {
		return nil, err
	}
	return liveIDs, nil
}

func (d *Dispatcher) autoScalingGroupWarmPoolInstanceIDs(ctx context.Context, autoScalingGroupName string) ([]string, error) {
	return d.autoScalingGroupWarmPoolInstanceIDsForMode(ctx, autoScalingGroupName, true)
}

func (d *Dispatcher) autoScalingGroupWarmPoolInstanceIDsReadOnly(ctx context.Context, autoScalingGroupName string) ([]string, error) {
	return d.autoScalingGroupWarmPoolInstanceIDsForMode(ctx, autoScalingGroupName, false)
}

func (d *Dispatcher) autoScalingGroupWarmPoolInstanceIDsForMode(ctx context.Context, autoScalingGroupName string, reconcile bool) ([]string, error) {
	instances, err := d.storage.RegisteredResources(types.ResourceTypeInstance)
	if err != nil {
		return nil, fmt.Errorf("retrieving registered instances: %w", err)
	}
	instanceIDs := make([]string, 0, len(instances))
	for _, instance := range instances {
		attrs, err := d.storage.ResourceAttributes(instance.ID)
		if err != nil {
			return nil, fmt.Errorf("retrieving instance attributes: %w", err)
		}
		groupName, _ := attrs.Key(attributeNameAutoScalingGroupName)
		if groupName == autoScalingGroupName && autoScalingInstanceIsWarm(attrs) {
			instanceIDs = append(instanceIDs, instance.ID)
		}
	}
	slices.Sort(instanceIDs)
	if len(instanceIDs) == 0 {
		return instanceIDs, nil
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

	liveIDs := make([]string, 0, len(instanceIDs))
	missingIDs := make([]string, 0)
	for _, instanceID := range instanceIDs {
		if _, ok := descriptionsByID[instanceID]; !ok {
			missingIDs = append(missingIDs, instanceID)
			continue
		}
		liveIDs = append(liveIDs, instanceID)
	}
	if len(missingIDs) == 0 {
		return liveIDs, nil
	}
	if !reconcile {
		return liveIDs, nil
	}
	if err := d.cleanupMissingAutoScalingInstances(ctx, missingIDs); err != nil {
		return nil, err
	}
	return liveIDs, nil
}

func autoScalingInstanceIsWarm(attrs storage.Attributes) bool {
	value, _ := attrs.Key(attributeNameAutoScalingInstanceWarmPool)
	isWarm, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return isWarm
}

func autoScalingInstanceNeedsReplacement(desc executor.InstanceDescription) bool {
	if desc.InstanceState.Name != api.InstanceStateRunning.Name {
		return true
	}
	return desc.HealthStatus == executor.InstanceHealthStatusUnhealthy
}

func autoScalingInstanceReplacementReason(desc executor.InstanceDescription) string {
	if desc.InstanceState.Name != api.InstanceStateRunning.Name {
		return desc.InstanceState.Name
	}
	if desc.HealthStatus == executor.InstanceHealthStatusUnhealthy {
		return "unhealthy"
	}
	return "unknown"
}

func (d *Dispatcher) cleanupMissingAutoScalingInstances(ctx context.Context, missingIDs []string) error {
	if len(missingIDs) == 0 {
		return nil
	}
	if err := d.cleanupDeleteOnTerminationVolumesForInstances(ctx, missingIDs); err != nil {
		return err
	}
	for _, instanceID := range missingIDs {
		d.cancelSpotReclaim(instanceID)
		if err := d.imds.ClearSpotInstanceAction(string(executorInstanceID(instanceID))); err != nil {
			api.Logger(ctx).Warn("failed to clear spot interruption action while cleaning missing auto scaling instance", "instance_id", instanceID, "error", err)
		}
		if err := d.closeSpotRequestForInstance(instanceID, spotRequestStatusServiceTerminatedCode, spotRequestStatusServiceTerminatedMsg); err != nil {
			api.Logger(ctx).Warn("failed to close spot request while cleaning missing auto scaling instance", "instance_id", instanceID, "error", err)
		}
		if err := d.storage.RemoveResource(instanceID); err != nil && !errors.As(err, &storage.ErrResourceNotFound{}) {
			return fmt.Errorf("removing missing auto scaling instance %s: %w", instanceID, err)
		}
		d.cleanupAutoScalingInstanceMetadata(ctx, instanceID)
	}
	return nil
}

func (d *Dispatcher) cleanupAutoScalingInstanceMetadata(ctx context.Context, instanceID string) {
	containerID := string(executorInstanceID(instanceID))
	if err := d.imds.SetEnabled(containerID, true); err != nil {
		api.Logger(ctx).Warn("failed to reset IMDS endpoint while reconciling auto scaling instance", "instance_id", instanceID, "error", err)
	}
	if err := d.imds.RevokeTokens(containerID); err != nil {
		api.Logger(ctx).Warn("failed to revoke IMDS tokens while reconciling auto scaling instance", "instance_id", instanceID, "error", err)
	}
	if err := d.imds.SetTags(containerID, nil); err != nil {
		api.Logger(ctx).Warn("failed to clear IMDS tags while reconciling auto scaling instance", "instance_id", instanceID, "error", err)
	}
}

func (d *Dispatcher) loadAutoScalingGroupData(ctx context.Context, autoScalingGroupName string) (*autoScalingGroupData, error) {
	if _, err := d.findResource(ctx, types.ResourceTypeAutoScalingGroup, autoScalingGroupName); err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("auto scaling group %q was not found", autoScalingGroupName))
		}
		return nil, err
	}

	attrs, err := d.storage.ResourceAttributes(autoScalingGroupName)
	if err != nil {
		return nil, fmt.Errorf("retrieving auto scaling group attributes: %w", err)
	}

	minSize, err := parseRequiredIntAttribute(attrs, attributeNameAutoScalingGroupMinSize)
	if err != nil {
		return nil, err
	}
	maxSize, err := parseRequiredIntAttribute(attrs, attributeNameAutoScalingGroupMaxSize)
	if err != nil {
		return nil, err
	}
	desiredCapacity, err := parseRequiredIntAttribute(attrs, attributeNameAutoScalingGroupDesiredCapacity)
	if err != nil {
		return nil, err
	}
	createdTimeStr, ok := attrs.Key(attributeNameAutoScalingGroupCreatedTime)
	if !ok || createdTimeStr == "" {
		return nil, fmt.Errorf("auto scaling group %s missing created time", autoScalingGroupName)
	}
	createdTime, err := parseTime(createdTimeStr)
	if err != nil {
		return nil, fmt.Errorf("invalid auto scaling group created time: %w", err)
	}
	launchTemplateID, ok := attrs.Key(attributeNameAutoScalingGroupLaunchTemplateID)
	if !ok || launchTemplateID == "" {
		return nil, fmt.Errorf("auto scaling group %s missing launch template id", autoScalingGroupName)
	}
	launchTemplateName, ok := attrs.Key(attributeNameAutoScalingGroupLaunchTemplateName)
	if !ok || launchTemplateName == "" {
		return nil, fmt.Errorf("auto scaling group %s missing launch template name", autoScalingGroupName)
	}
	launchTemplateVersion, _ := attrs.Key(attributeNameAutoScalingGroupLaunchTemplateVersion)
	if launchTemplateVersion == "" {
		launchTemplateVersion = "1"
	}
	launchTemplateImageID, _ := attrs.Key(attributeNameAutoScalingGroupLaunchTemplateImageID)
	launchTemplateInstanceType, _ := attrs.Key(attributeNameAutoScalingGroupLaunchTemplateType)
	launchTemplateUserData, _ := attrs.Key(attributeNameAutoScalingGroupLaunchTemplateUserData)
	launchTemplateBlockDeviceMappingsRaw, _ := attrs.Key(attributeNameAutoScalingGroupLaunchTemplateBlockDeviceMappings)
	launchTemplateBlockDeviceMappings, err := unmarshalBlockDeviceMappings(launchTemplateBlockDeviceMappingsRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid auto scaling group launch template block device mappings: %w", err)
	}
	defaultCooldown, err := parseRequiredIntAttribute(attrs, attributeNameAutoScalingGroupDefaultCooldown)
	if err != nil {
		return nil, err
	}
	healthCheckType, _ := attrs.Key(attributeNameAutoScalingGroupHealthCheckType)
	if healthCheckType == "" {
		healthCheckType = autoScalingHealthCheckType
	}

	var vpcZoneIdentifier *string
	if v, ok := attrs.Key(attributeNameAutoScalingGroupVPCZoneIdentifier); ok {
		vpcZoneIdentifier = &v
	}

	warmPoolEnabled, err := parseOptionalBoolAttribute(attrs, attributeNameAutoScalingGroupWarmPoolEnabled, false)
	if err != nil {
		return nil, err
	}
	warmPoolMinSize, err := parseOptionalIntAttribute(attrs, attributeNameAutoScalingGroupWarmPoolMinSize, 0)
	if err != nil {
		return nil, err
	}
	warmPoolMaxGroupPreparedCapacityValue, hasWarmPoolMaxGroupPreparedCapacity, err := parseOptionalIntPtrAttribute(
		attrs,
		attributeNameAutoScalingGroupWarmPoolMaxGroupPreparedCapacity,
	)
	if err != nil {
		return nil, err
	}
	var warmPoolMaxGroupPreparedCapacity *int
	if hasWarmPoolMaxGroupPreparedCapacity {
		warmPoolMaxGroupPreparedCapacity = &warmPoolMaxGroupPreparedCapacityValue
	}
	warmPoolState, _ := attrs.Key(attributeNameAutoScalingGroupWarmPoolState)
	if warmPoolState == "" {
		warmPoolState = warmPoolStateStopped
	}
	if warmPoolState, err = parseWarmPoolState(warmPoolState); err != nil {
		return nil, err
	}
	warmPoolStatus, _ := attrs.Key(attributeNameAutoScalingGroupWarmPoolStatus)
	if warmPoolEnabled && warmPoolStatus == "" {
		warmPoolStatus = warmPoolStatusActive
	}
	warmPoolReuseOnScaleInValue, hasWarmPoolReuseOnScaleIn, err := parseOptionalBoolPtrAttribute(
		attrs,
		attributeNameAutoScalingGroupWarmPoolReuseOnScaleIn,
	)
	if err != nil {
		return nil, err
	}
	var warmPoolReuseOnScaleIn *bool
	if hasWarmPoolReuseOnScaleIn {
		warmPoolReuseOnScaleIn = &warmPoolReuseOnScaleInValue
	}

	return &autoScalingGroupData{
		Name:                              autoScalingGroupName,
		MinSize:                           minSize,
		MaxSize:                           maxSize,
		DesiredCapacity:                   desiredCapacity,
		CreatedTime:                       createdTime,
		LaunchTemplateID:                  launchTemplateID,
		LaunchTemplateName:                launchTemplateName,
		LaunchTemplateVersion:             launchTemplateVersion,
		LaunchTemplateImageID:             launchTemplateImageID,
		LaunchTemplateInstanceType:        launchTemplateInstanceType,
		LaunchTemplateUserData:            launchTemplateUserData,
		LaunchTemplateBlockDeviceMappings: launchTemplateBlockDeviceMappings,
		VPCZoneIdentifier:                 vpcZoneIdentifier,
		DefaultCooldown:                   defaultCooldown,
		HealthCheckType:                   healthCheckType,
		WarmPoolEnabled:                   warmPoolEnabled,
		WarmPoolMinSize:                   warmPoolMinSize,
		WarmPoolMaxGroupPreparedCapacity:  warmPoolMaxGroupPreparedCapacity,
		WarmPoolState:                     warmPoolState,
		WarmPoolStatus:                    warmPoolStatus,
		WarmPoolReuseOnScaleIn:            warmPoolReuseOnScaleIn,
	}, nil
}

func (d *Dispatcher) saveAutoScalingGroupData(group *autoScalingGroupData) error {
	warmPoolMaxGroupPreparedCapacity := ""
	if group.WarmPoolMaxGroupPreparedCapacity != nil {
		warmPoolMaxGroupPreparedCapacity = strconv.Itoa(*group.WarmPoolMaxGroupPreparedCapacity)
	}
	warmPoolReuseOnScaleIn := ""
	if group.WarmPoolReuseOnScaleIn != nil {
		warmPoolReuseOnScaleIn = strconv.FormatBool(*group.WarmPoolReuseOnScaleIn)
	}
	attrs := []storage.Attribute{
		{Key: attributeNameAutoScalingGroupName, Value: group.Name},
		{Key: attributeNameAutoScalingGroupMinSize, Value: strconv.Itoa(group.MinSize)},
		{Key: attributeNameAutoScalingGroupMaxSize, Value: strconv.Itoa(group.MaxSize)},
		{Key: attributeNameAutoScalingGroupDesiredCapacity, Value: strconv.Itoa(group.DesiredCapacity)},
		{Key: attributeNameAutoScalingGroupCreatedTime, Value: group.CreatedTime.Format(time.RFC3339Nano)},
		{Key: attributeNameAutoScalingGroupLaunchTemplateID, Value: group.LaunchTemplateID},
		{Key: attributeNameAutoScalingGroupLaunchTemplateName, Value: group.LaunchTemplateName},
		{Key: attributeNameAutoScalingGroupLaunchTemplateVersion, Value: group.LaunchTemplateVersion},
		{Key: attributeNameAutoScalingGroupLaunchTemplateImageID, Value: group.LaunchTemplateImageID},
		{Key: attributeNameAutoScalingGroupLaunchTemplateType, Value: group.LaunchTemplateInstanceType},
		{Key: attributeNameAutoScalingGroupLaunchTemplateUserData, Value: group.LaunchTemplateUserData},
		{Key: attributeNameAutoScalingGroupDefaultCooldown, Value: strconv.Itoa(group.DefaultCooldown)},
		{Key: attributeNameAutoScalingGroupHealthCheckType, Value: group.HealthCheckType},
		{Key: attributeNameAutoScalingGroupWarmPoolEnabled, Value: strconv.FormatBool(group.WarmPoolEnabled)},
		{Key: attributeNameAutoScalingGroupWarmPoolMinSize, Value: strconv.Itoa(group.WarmPoolMinSize)},
		{Key: attributeNameAutoScalingGroupWarmPoolState, Value: group.WarmPoolState},
		{Key: attributeNameAutoScalingGroupWarmPoolStatus, Value: group.WarmPoolStatus},
		{Key: attributeNameAutoScalingGroupWarmPoolMaxGroupPreparedCapacity, Value: warmPoolMaxGroupPreparedCapacity},
		{Key: attributeNameAutoScalingGroupWarmPoolReuseOnScaleIn, Value: warmPoolReuseOnScaleIn},
	}
	if len(group.LaunchTemplateBlockDeviceMappings) > 0 {
		raw, err := marshalBlockDeviceMappings(group.LaunchTemplateBlockDeviceMappings)
		if err != nil {
			return fmt.Errorf("marshaling auto scaling launch template block device mappings: %w", err)
		}
		attrs = append(attrs, storage.Attribute{
			Key:   attributeNameAutoScalingGroupLaunchTemplateBlockDeviceMappings,
			Value: raw,
		})
	}
	if group.VPCZoneIdentifier != nil {
		attrs = append(attrs, storage.Attribute{Key: attributeNameAutoScalingGroupVPCZoneIdentifier, Value: *group.VPCZoneIdentifier})
	}
	if err := d.storage.SetResourceAttributes(group.Name, attrs); err != nil {
		return fmt.Errorf("saving auto scaling group attributes: %w", err)
	}
	return nil
}

func (d *Dispatcher) apiAutoScalingGroup(ctx context.Context, group *autoScalingGroupData, includeInstances bool) (api.AutoScalingGroup, error) {
	name := group.Name
	defaultCooldown := group.DefaultCooldown
	desiredCapacity := group.DesiredCapacity
	healthCheckType := group.HealthCheckType
	maxSize := group.MaxSize
	minSize := group.MinSize
	launchTemplateID := group.LaunchTemplateID
	launchTemplateName := group.LaunchTemplateName
	launchTemplateVersion := group.LaunchTemplateVersion
	availabilityZones := []string{defaultAvailabilityZone(d.opts.Region)}

	out := api.AutoScalingGroup{
		AutoScalingGroupName: &name,
		CreatedTime:          &group.CreatedTime,
		DefaultCooldown:      &defaultCooldown,
		DesiredCapacity:      &desiredCapacity,
		HealthCheckType:      &healthCheckType,
		LaunchTemplate: &api.AutoScalingLaunchTemplateSpecification{
			LaunchTemplateID:   &launchTemplateID,
			LaunchTemplateName: &launchTemplateName,
			Version:            &launchTemplateVersion,
		},
		MaxSize:           &maxSize,
		MinSize:           &minSize,
		VPCZoneIdentifier: group.VPCZoneIdentifier,
		AvailabilityZones: availabilityZones,
	}
	if group.WarmPoolEnabled {
		warmPoolInstanceIDs, err := d.autoScalingGroupWarmPoolInstanceIDsReadOnly(ctx, group.Name)
		if err != nil {
			return api.AutoScalingGroup{}, err
		}
		warmPoolSize := len(warmPoolInstanceIDs)
		minSize := group.WarmPoolMinSize
		poolState := group.WarmPoolState
		if poolState == "" {
			poolState = warmPoolStateStopped
		}
		status := group.WarmPoolStatus
		if status == "" {
			status = warmPoolStatusActive
		}
		out.WarmPoolConfiguration = &api.WarmPoolConfiguration{
			MinSize:   &minSize,
			PoolState: &poolState,
			Status:    &status,
		}
		out.WarmPoolSize = &warmPoolSize
		if group.WarmPoolMaxGroupPreparedCapacity != nil {
			value := *group.WarmPoolMaxGroupPreparedCapacity
			out.WarmPoolConfiguration.MaxGroupPreparedCapacity = &value
		}
		if group.WarmPoolReuseOnScaleIn != nil {
			reuseOnScaleIn := *group.WarmPoolReuseOnScaleIn
			out.WarmPoolConfiguration.InstanceReusePolicy = &api.WarmPoolInstanceReusePolicy{
				ReuseOnScaleIn: &reuseOnScaleIn,
			}
		}
	}

	groupAttrs, err := d.storage.ResourceAttributes(group.Name)
	if err != nil {
		return api.AutoScalingGroup{}, fmt.Errorf("retrieving auto scaling group attributes: %w", err)
	}
	autoScalingTagResourceID := group.Name
	autoScalingTagPropagateAtLaunch := false
	autoScalingTags := make([]api.AutoScalingTagDescription, 0)
	for _, attr := range groupAttrs {
		if !attr.IsTag() {
			continue
		}
		tagKey := attr.TagKey()
		tagValue := attr.Value
		autoScalingTagResourceTypeCopy := autoScalingTagResourceType
		autoScalingTags = append(autoScalingTags, api.AutoScalingTagDescription{
			Key:               &tagKey,
			Value:             &tagValue,
			PropagateAtLaunch: &autoScalingTagPropagateAtLaunch,
			ResourceID:        &autoScalingTagResourceID,
			ResourceType:      &autoScalingTagResourceTypeCopy,
		})
	}
	slices.SortFunc(autoScalingTags, func(a, b api.AutoScalingTagDescription) int {
		return cmp.Compare(*a.Key, *b.Key)
	})
	out.Tags = autoScalingTags

	if includeInstances {
		instanceIDs, err := d.autoScalingGroupInstanceIDsReadOnly(ctx, group.Name)
		if err != nil {
			return api.AutoScalingGroup{}, err
		}
		instances := make([]api.AutoScalingInstance, 0, len(instanceIDs))
		for _, instanceID := range instanceIDs {
			attrs, err := d.storage.ResourceAttributes(instanceID)
			if err != nil {
				return api.AutoScalingGroup{}, fmt.Errorf("retrieving instance attributes: %w", err)
			}
			availabilityZoneStr, _ := attrs.Key(attributeNameAvailabilityZone)
			instanceTypeStr, _ := attrs.Key(attributeNameAutoScalingGroupInstanceType)
			healthStatus := autoScalingHealthStatus
			lifecycleState := autoScalingLifecycleState
			protectedFromScaleIn := false

			instanceIDCopy := instanceID
			availabilityZone := availabilityZoneStr
			instanceType := instanceTypeStr
			instanceLaunchTemplateID := launchTemplateID
			instanceLaunchTemplateName := launchTemplateName
			instanceLaunchTemplateVersion := launchTemplateVersion

			instances = append(instances, api.AutoScalingInstance{
				AvailabilityZone: &availabilityZone,
				HealthStatus:     &healthStatus,
				InstanceID:       &instanceIDCopy,
				InstanceType:     &instanceType,
				LaunchTemplate: &api.AutoScalingLaunchTemplateSpecification{
					LaunchTemplateID:   &instanceLaunchTemplateID,
					LaunchTemplateName: &instanceLaunchTemplateName,
					Version:            &instanceLaunchTemplateVersion,
				},
				LifecycleState:       lifecycleState,
				ProtectedFromScaleIn: &protectedFromScaleIn,
			})
		}
		out.Instances = instances
	}

	return out, nil
}

func parseWarmPoolState(state string) (string, error) {
	switch state {
	case warmPoolStateStopped, warmPoolStateRunning, warmPoolStateHibernated:
		return state, nil
	default:
		return "", api.InvalidParameterValueError("PoolState", state)
	}
}

func autoScalingWarmPoolLifecycleState(instanceState string, poolState string) string {
	switch instanceState {
	case api.InstanceStateRunning.Name:
		return autoScalingWarmLifecycleStateRunning
	case api.InstanceStateStopped.Name:
		if poolState == warmPoolStateHibernated {
			return autoScalingWarmLifecycleStateHibernated
		}
		return autoScalingWarmLifecycleStateStopped
	case api.InstanceStatePending.Name:
		return autoScalingWarmLifecycleStatePending
	default:
		return autoScalingWarmLifecycleStateStopped
	}
}

func validateAutoScalingGroupSizes(minSize int, maxSize int) error {
	if minSize < 0 {
		return api.ErrWithCode("ValidationError", fmt.Errorf("MinSize must be >= 0"))
	}
	if maxSize < 0 {
		return api.ErrWithCode("ValidationError", fmt.Errorf("MaxSize must be >= 0"))
	}
	if minSize > maxSize {
		return api.ErrWithCode("ValidationError", fmt.Errorf("MinSize cannot be greater than MaxSize"))
	}
	return nil
}

func validateDesiredCapacity(desiredCapacity int, minSize int, maxSize int) error {
	if desiredCapacity < minSize || desiredCapacity > maxSize {
		return api.ErrWithCode("ValidationError", fmt.Errorf("DesiredCapacity must be between MinSize and MaxSize"))
	}
	return nil
}

func parseRequiredIntAttribute(attrs storage.Attributes, key string) (int, error) {
	v, ok := attrs.Key(key)
	if !ok || v == "" {
		return 0, fmt.Errorf("missing required attribute %s", key)
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer attribute %s: %w", key, err)
	}
	return i, nil
}

func parseOptionalIntAttribute(attrs storage.Attributes, key string, fallback int) (int, error) {
	v, ok := attrs.Key(key)
	if !ok || v == "" {
		return fallback, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer attribute %s: %w", key, err)
	}
	return i, nil
}

func parseOptionalIntPtrAttribute(attrs storage.Attributes, key string) (int, bool, error) {
	v, ok := attrs.Key(key)
	if !ok || v == "" {
		return 0, false, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("invalid integer attribute %s: %w", key, err)
	}
	return i, true, nil
}

func parseOptionalBoolAttribute(attrs storage.Attributes, key string, fallback bool) (bool, error) {
	v, ok := attrs.Key(key)
	if !ok || v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid boolean attribute %s: %w", key, err)
	}
	return b, nil
}

func parseOptionalBoolPtrAttribute(attrs storage.Attributes, key string) (bool, bool, error) {
	v, ok := attrs.Key(key)
	if !ok || v == "" {
		return false, false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false, fmt.Errorf("invalid boolean attribute %s: %w", key, err)
	}
	return b, true, nil
}
