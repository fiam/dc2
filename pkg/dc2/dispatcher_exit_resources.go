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

type ownedResourceLeakReport struct {
	autoScalingGroups []string
	instances         []string
	launchTemplates   []string
	ownedContainers   []string
	volumes           []string
}

func (r ownedResourceLeakReport) empty() bool {
	return len(r.autoScalingGroups) == 0 &&
		len(r.instances) == 0 &&
		len(r.launchTemplates) == 0 &&
		len(r.ownedContainers) == 0 &&
		len(r.volumes) == 0
}

func (r ownedResourceLeakReport) String() string {
	parts := make([]string, 0, 5)
	if len(r.autoScalingGroups) > 0 {
		parts = append(parts, fmt.Sprintf("auto-scaling-groups=[%s]", strings.Join(r.autoScalingGroups, ",")))
	}
	if len(r.instances) > 0 {
		parts = append(parts, fmt.Sprintf("instances=[%s]", strings.Join(r.instances, ",")))
	}
	if len(r.ownedContainers) > 0 {
		parts = append(parts, fmt.Sprintf("owned-instance-containers=[%s]", strings.Join(r.ownedContainers, ",")))
	}
	if len(r.launchTemplates) > 0 {
		parts = append(parts, fmt.Sprintf("launch-templates=[%s]", strings.Join(r.launchTemplates, ",")))
	}
	if len(r.volumes) > 0 {
		parts = append(parts, fmt.Sprintf("volumes=[%s]", strings.Join(r.volumes, ",")))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func (d *Dispatcher) cleanupOwnedResources(ctx context.Context) error {
	api.Logger(ctx).Info("starting owned resource cleanup on exit")
	var cleanupErr error

	if err := d.cleanupOwnedAutoScalingGroups(ctx); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := d.cleanupOwnedInstanceContainers(ctx); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := d.removeAllResourcesOfType(ctx, types.ResourceTypeAutoScalingGroup); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := d.removeAllResourcesOfType(ctx, types.ResourceTypeInstance); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := d.removeAllResourcesOfType(ctx, types.ResourceTypeVolume); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := d.removeAllResourcesOfType(ctx, types.ResourceTypeLaunchTemplate); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := d.assertNoOwnedResources(ctx); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if cleanupErr == nil {
		api.Logger(ctx).Info("completed owned resource cleanup on exit")
	}
	return cleanupErr
}

func (d *Dispatcher) cleanupOwnedAutoScalingGroups(ctx context.Context) error {
	resources, err := d.storage.RegisteredResources(types.ResourceTypeAutoScalingGroup)
	if err != nil {
		return fmt.Errorf("listing auto scaling groups for exit cleanup: %w", err)
	}
	api.Logger(ctx).Info("cleaning auto scaling groups on exit", "count", len(resources))
	forceDelete := true
	var cleanupErr error
	for _, resource := range resources {
		if _, err := d.dispatchDeleteAutoScalingGroup(ctx, &api.DeleteAutoScalingGroupRequest{
			AutoScalingGroupName: resource.ID,
			ForceDelete:          &forceDelete,
		}); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("deleting auto scaling group %s: %w", resource.ID, err))
		}
	}
	return cleanupErr
}

func (d *Dispatcher) cleanupOwnedInstanceContainers(ctx context.Context) error {
	ownedInstanceIDs, err := d.exe.ListOwnedInstances(ctx)
	if err != nil {
		return fmt.Errorf("listing owned instance containers for exit cleanup: %w", err)
	}
	containerIDs := make([]string, 0, len(ownedInstanceIDs))
	for _, ownedInstanceID := range ownedInstanceIDs {
		containerIDs = append(containerIDs, apiInstanceID(ownedInstanceID))
	}
	api.Logger(ctx).Info(
		"cleaning owned instance containers on exit",
		"count",
		len(containerIDs),
		"instance_ids",
		containerIDs,
	)
	var cleanupErr error
	for _, ownedInstanceID := range ownedInstanceIDs {
		if _, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{
			InstanceIDs: []executor.InstanceID{ownedInstanceID},
		}); err != nil {
			var apiErr *api.Error
			if errors.As(err, &apiErr) && apiErr.Code == api.ErrorCodeInstanceNotFound {
				continue
			}
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("terminating owned instance container %s: %w", ownedInstanceID, err))
			continue
		}
		d.cleanupAutoScalingInstanceMetadata(ctx, apiInstanceID(ownedInstanceID))
	}
	return cleanupErr
}

func (d *Dispatcher) removeAllResourcesOfType(ctx context.Context, resourceType types.ResourceType) error {
	resources, err := d.storage.RegisteredResources(resourceType)
	if err != nil {
		return fmt.Errorf("listing %s resources for exit cleanup: %w", resourceType, err)
	}
	if len(resources) == 0 {
		return nil
	}
	resourceIDs := make([]string, 0, len(resources))
	for _, resource := range resources {
		resourceIDs = append(resourceIDs, resource.ID)
	}
	api.Logger(ctx).Info(
		"removing resource records from storage on exit",
		"resource_type",
		string(resourceType),
		"count",
		len(resourceIDs),
		"resource_ids",
		resourceIDs,
	)
	var removeErr error
	for _, resource := range resources {
		if err := d.storage.RemoveResource(resource.ID); err != nil && !errors.As(err, &storage.ErrResourceNotFound{}) {
			removeErr = errors.Join(removeErr, fmt.Errorf("removing %s resource %s: %w", resourceType, resource.ID, err))
		}
	}
	return removeErr
}

func (d *Dispatcher) assertNoOwnedResources(ctx context.Context) error {
	report, err := d.ownedResourceLeakReport(ctx)
	if err != nil {
		return err
	}
	if report.empty() {
		api.Logger(ctx).Info("verified that no owned resources remain on exit")
		return nil
	}
	api.Logger(ctx).Info("owned resources remain on exit", "report", report.String())
	return fmt.Errorf("owned resources remain on exit: %s", report.String())
}

func (d *Dispatcher) ownedResourceLeakReport(ctx context.Context) (ownedResourceLeakReport, error) {
	var report ownedResourceLeakReport

	autoScalingGroups, err := d.storage.RegisteredResources(types.ResourceTypeAutoScalingGroup)
	if err != nil {
		return report, fmt.Errorf("listing auto scaling groups for exit verification: %w", err)
	}
	for _, resource := range autoScalingGroups {
		report.autoScalingGroups = append(report.autoScalingGroups, resource.ID)
	}

	instances, err := d.storage.RegisteredResources(types.ResourceTypeInstance)
	if err != nil {
		return report, fmt.Errorf("listing instances for exit verification: %w", err)
	}
	for _, resource := range instances {
		attrs, err := d.storage.ResourceAttributes(resource.ID)
		if err != nil {
			if errors.As(err, &storage.ErrResourceNotFound{}) {
				continue
			}
			return report, fmt.Errorf("retrieving instance attributes for exit verification: %w", err)
		}
		if terminatedAt, _ := attrs.Key(attributeNameInstanceTerminatedAt); terminatedAt != "" {
			continue
		}
		report.instances = append(report.instances, resource.ID)
	}

	launchTemplates, err := d.storage.RegisteredResources(types.ResourceTypeLaunchTemplate)
	if err != nil {
		return report, fmt.Errorf("listing launch templates for exit verification: %w", err)
	}
	for _, resource := range launchTemplates {
		report.launchTemplates = append(report.launchTemplates, resource.ID)
	}

	volumes, err := d.storage.RegisteredResources(types.ResourceTypeVolume)
	if err != nil {
		return report, fmt.Errorf("listing volumes for exit verification: %w", err)
	}
	for _, resource := range volumes {
		report.volumes = append(report.volumes, resource.ID)
	}

	ownedContainerIDs, err := d.exe.ListOwnedInstances(ctx)
	if err != nil {
		return report, fmt.Errorf("listing owned instance containers for exit verification: %w", err)
	}
	for _, ownedContainerID := range ownedContainerIDs {
		report.ownedContainers = append(report.ownedContainers, apiInstanceID(ownedContainerID))
	}

	return report, nil
}
