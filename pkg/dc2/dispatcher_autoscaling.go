package dc2

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	autoScalingTagResourceType                                     = "auto-scaling-group"

	autoScalingDefaultCooldown = 300
	autoScalingHealthStatus    = "Healthy"
	autoScalingHealthCheckType = "EC2"
	autoScalingLifecycleState  = "InService"
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
		if err := d.reconcileAutoScalingGroup(ctx, group); err != nil {
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
	if err := d.scaleAutoScalingGroupTo(ctx, group, group.DesiredCapacity); err != nil {
		return nil, err
	}
	return &api.UpdateAutoScalingGroupResponse{}, nil
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
	if req.ShouldDecrementDesiredCapacity != nil && *req.ShouldDecrementDesiredCapacity {
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

	instanceIDs, err := d.autoScalingGroupInstanceIDs(ctx, req.AutoScalingGroupName)
	if err != nil {
		return nil, err
	}
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

func (d *Dispatcher) scaleAutoScalingGroupTo(ctx context.Context, group *autoScalingGroupData, desiredCapacity int) error {
	if err := validateDesiredCapacity(desiredCapacity, group.MinSize, group.MaxSize); err != nil {
		return err
	}
	instanceIDs, err := d.autoScalingGroupInstanceIDs(ctx, group.Name)
	if err != nil {
		return err
	}

	switch {
	case len(instanceIDs) < desiredCapacity:
		if err := d.scaleOutAutoScalingGroup(ctx, group, desiredCapacity-len(instanceIDs)); err != nil {
			return err
		}
	case len(instanceIDs) > desiredCapacity:
		redundant := len(instanceIDs) - desiredCapacity
		slices.Sort(instanceIDs)
		if err := d.terminateAutoScalingInstances(ctx, instanceIDs[:redundant]); err != nil {
			return err
		}
	}

	group.DesiredCapacity = desiredCapacity
	if err := d.saveAutoScalingGroupData(group); err != nil {
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
	return nil
}

func (d *Dispatcher) terminateAutoScalingInstances(ctx context.Context, instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return nil
	}
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
		if err := d.storage.RemoveResource(instanceID); err != nil && !errors.As(err, &storage.ErrResourceNotFound{}) {
			return fmt.Errorf("removing auto scaling instance %s: %w", instanceID, err)
		}
		d.cleanupAutoScalingInstanceMetadata(ctx, instanceID)
	}
	return nil
}

func (d *Dispatcher) autoScalingGroupInstanceIDs(ctx context.Context, autoScalingGroupName string) ([]string, error) {
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
		if groupName == autoScalingGroupName {
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
	for _, instanceID := range instanceIDs {
		desc, ok := descriptionsByID[instanceID]
		if !ok {
			missingIDs = append(missingIDs, instanceID)
			continue
		}
		if autoScalingInstanceNeedsReplacement(desc) {
			replaceIDs = append(replaceIDs, instanceID)
			continue
		}
		liveIDs = append(liveIDs, instanceID)
	}

	if len(replaceIDs) > 0 {
		if err := d.terminateAutoScalingInstances(ctx, replaceIDs); err != nil {
			return nil, err
		}
	}
	if len(missingIDs) == 0 {
		return liveIDs, nil
	}
	if err := d.cleanupMissingAutoScalingInstances(ctx, missingIDs); err != nil {
		return nil, err
	}
	return liveIDs, nil
}

func autoScalingInstanceNeedsReplacement(desc executor.InstanceDescription) bool {
	if desc.InstanceState.Name != api.InstanceStateRunning.Name {
		return true
	}
	return desc.HealthStatus == executor.InstanceHealthStatusUnhealthy
}

func (d *Dispatcher) cleanupMissingAutoScalingInstances(ctx context.Context, missingIDs []string) error {
	if len(missingIDs) == 0 {
		return nil
	}
	if err := d.cleanupDeleteOnTerminationVolumesForInstances(ctx, missingIDs); err != nil {
		return err
	}
	for _, instanceID := range missingIDs {
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
	}, nil
}

func (d *Dispatcher) saveAutoScalingGroupData(group *autoScalingGroupData) error {
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
		instanceIDs, err := d.autoScalingGroupInstanceIDs(ctx, group.Name)
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
