package dc2

import (
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
	attributeNameAutoScalingGroupName                  = "AutoScalingGroupName"
	attributeNameAutoScalingGroupMinSize               = "AutoScalingGroupMinSize"
	attributeNameAutoScalingGroupMaxSize               = "AutoScalingGroupMaxSize"
	attributeNameAutoScalingGroupDesiredCapacity       = "AutoScalingGroupDesiredCapacity"
	attributeNameAutoScalingGroupCreatedTime           = "AutoScalingGroupCreatedTime"
	attributeNameAutoScalingGroupLaunchTemplateID      = "AutoScalingGroupLaunchTemplateID"
	attributeNameAutoScalingGroupLaunchTemplateName    = "AutoScalingGroupLaunchTemplateName"
	attributeNameAutoScalingGroupLaunchTemplateVersion = "AutoScalingGroupLaunchTemplateVersion"
	attributeNameAutoScalingGroupLaunchTemplateImageID = "AutoScalingGroupLaunchTemplateImageID"
	attributeNameAutoScalingGroupLaunchTemplateType    = "AutoScalingGroupLaunchTemplateInstanceType"
	attributeNameAutoScalingGroupVPCZoneIdentifier     = "AutoScalingGroupVPCZoneIdentifier"
	attributeNameAutoScalingGroupDefaultCooldown       = "AutoScalingGroupDefaultCooldown"
	attributeNameAutoScalingGroupHealthCheckType       = "AutoScalingGroupHealthCheckType"
	attributeNameAutoScalingGroupInstanceType          = "AutoScalingGroupInstanceType"

	autoScalingDefaultCooldown = 300
	autoScalingHealthStatus    = "Healthy"
	autoScalingHealthCheckType = "EC2"
	autoScalingLifecycleState  = "InService"
)

type autoScalingGroupData struct {
	Name                       string
	MinSize                    int
	MaxSize                    int
	DesiredCapacity            int
	CreatedTime                time.Time
	LaunchTemplateID           string
	LaunchTemplateName         string
	LaunchTemplateVersion      string
	LaunchTemplateImageID      string
	LaunchTemplateInstanceType string
	VPCZoneIdentifier          *string
	DefaultCooldown            int
	HealthCheckType            string
}

func (d *Dispatcher) dispatchCreateAutoScalingGroup(ctx context.Context, req *api.CreateAutoScalingGroupRequest) (*api.CreateAutoScalingGroupResponse, error) {
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
		Name:                       req.AutoScalingGroupName,
		MinSize:                    req.MinSize,
		MaxSize:                    req.MaxSize,
		DesiredCapacity:            desiredCapacity,
		CreatedTime:                time.Now().UTC(),
		LaunchTemplateID:           lt.ID,
		LaunchTemplateName:         lt.Name,
		LaunchTemplateVersion:      lt.Version,
		LaunchTemplateImageID:      lt.ImageID,
		LaunchTemplateInstanceType: lt.InstanceType,
		VPCZoneIdentifier:          req.VPCZoneIdentifier,
		DefaultCooldown:            autoScalingDefaultCooldown,
		HealthCheckType:            autoScalingHealthCheckType,
	}
	if err := d.saveAutoScalingGroupData(&group); err != nil {
		_ = d.storage.RemoveResource(req.AutoScalingGroupName)
		return nil, err
	}
	if err := d.scaleAutoScalingGroupTo(ctx, &group, desiredCapacity); err != nil {
		_ = d.storage.RemoveResource(req.AutoScalingGroupName)
		return nil, err
	}
	return &api.CreateAutoScalingGroupResponse{}, nil
}

func (d *Dispatcher) dispatchDescribeAutoScalingGroups(ctx context.Context, req *api.DescribeAutoScalingGroupsRequest) (*api.DescribeAutoScalingGroupsResponse, error) {
	resources, err := d.storage.RegisteredResources(types.ResourceTypeAutoScalingGroup)
	if err != nil {
		return nil, fmt.Errorf("retrieving auto scaling groups: %w", err)
	}

	filter := make(map[string]struct{}, len(req.AutoScalingGroupNames))
	for _, name := range req.AutoScalingGroupNames {
		filter[name] = struct{}{}
	}

	groups := make([]api.AutoScalingGroup, 0, len(resources))
	includeInstances := req.IncludeInstances == nil || *req.IncludeInstances
	for _, r := range resources {
		if len(filter) > 0 {
			if _, ok := filter[r.ID]; !ok {
				continue
			}
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

func (d *Dispatcher) scaleOutAutoScalingGroup(ctx context.Context, group *autoScalingGroupData, count int) error {
	created, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      group.LaunchTemplateImageID,
		InstanceType: group.LaunchTemplateInstanceType,
		Count:        count,
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
		if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
			return fmt.Errorf("setting auto scaling instance attributes: %w", err)
		}
	}

	if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{InstanceIDs: created}); err != nil {
		return executorError(err)
	}
	return nil
}

func (d *Dispatcher) terminateAutoScalingInstances(ctx context.Context, instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return nil
	}
	if _, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{InstanceIDs: executorInstanceIDs(instanceIDs)}); err != nil {
		return executorError(err)
	}
	for _, instanceID := range instanceIDs {
		if err := d.storage.RemoveResource(instanceID); err != nil {
			return fmt.Errorf("removing auto scaling instance %s: %w", instanceID, err)
		}
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
	return instanceIDs, nil
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
		Name:                       autoScalingGroupName,
		MinSize:                    minSize,
		MaxSize:                    maxSize,
		DesiredCapacity:            desiredCapacity,
		CreatedTime:                createdTime,
		LaunchTemplateID:           launchTemplateID,
		LaunchTemplateName:         launchTemplateName,
		LaunchTemplateVersion:      launchTemplateVersion,
		LaunchTemplateImageID:      launchTemplateImageID,
		LaunchTemplateInstanceType: launchTemplateInstanceType,
		VPCZoneIdentifier:          vpcZoneIdentifier,
		DefaultCooldown:            defaultCooldown,
		HealthCheckType:            healthCheckType,
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
		{Key: attributeNameAutoScalingGroupDefaultCooldown, Value: strconv.Itoa(group.DefaultCooldown)},
		{Key: attributeNameAutoScalingGroupHealthCheckType, Value: group.HealthCheckType},
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
