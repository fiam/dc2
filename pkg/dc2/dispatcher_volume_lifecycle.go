package dc2

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

func (d *Dispatcher) attachInstanceBlockDeviceMappings(ctx context.Context, ids []executor.InstanceID, availabilityZone string, mappings []api.RunInstancesBlockDeviceMapping) error {
	for _, instanceID := range ids {
		apiID := apiInstanceID(instanceID)
		for _, mapping := range mappings {
			if mapping.EBS == nil {
				return api.InvalidParameterValueError("BlockDeviceMapping.Ebs", "<empty>")
			}

			volumeType := mapping.EBS.VolumeType
			if volumeType == "" {
				volumeType = types.VolumeTypeGp3
			}
			createReq := &api.CreateVolumeRequest{
				AvailabilityZone: availabilityZone,
				Encrypted:        mapping.EBS.Encrypted,
				Iops:             mapping.EBS.Iops,
				KmsKeyID:         mapping.EBS.KmsKeyID,
				Size:             mapping.EBS.VolumeSize,
				Throughput:       mapping.EBS.Throughput,
				VolumeType:       volumeType,
			}
			volumeResp, err := d.dispatchCreateVolume(ctx, createReq)
			if err != nil {
				return fmt.Errorf("creating volume for instance %s device %s: %w", apiID, mapping.DeviceName, err)
			}
			if volumeResp == nil || volumeResp.VolumeID == nil || *volumeResp.VolumeID == "" {
				return fmt.Errorf("creating volume for instance %s device %s: missing volume id in response", apiID, mapping.DeviceName)
			}
			volumeID := *volumeResp.VolumeID

			_, err = d.dispatchAttachVolume(ctx, &api.AttachVolumeRequest{
				Device:     mapping.DeviceName,
				InstanceID: apiID,
				VolumeID:   volumeID,
			})
			if err != nil {
				_, deleteErr := d.dispatchDeleteVolume(ctx, &api.DeleteVolumeRequest{VolumeID: volumeID})
				if deleteErr != nil {
					err = errors.Join(err, fmt.Errorf("cleaning up unattached volume %s: %w", volumeID, deleteErr))
				}
				return fmt.Errorf("attaching volume %s to instance %s device %s: %w", volumeID, apiID, mapping.DeviceName, err)
			}

			err = d.storage.SetResourceAttributes(volumeID, []storage.Attribute{
				{Key: attributeNameVolumeDeleteOnTermination, Value: strconv.FormatBool(mapping.EBS.DeleteOnTermination)},
				{Key: attributeNameVolumeDeleteOnTerminationInstanceID, Value: apiID},
			})
			if err != nil {
				_, deleteErr := d.dispatchDeleteVolume(ctx, &api.DeleteVolumeRequest{VolumeID: volumeID})
				if deleteErr != nil {
					err = errors.Join(err, fmt.Errorf("cleaning up volume %s after metadata failure: %w", volumeID, deleteErr))
				}
				return fmt.Errorf("setting delete-on-termination metadata for volume %s: %w", volumeID, err)
			}
		}
	}
	return nil
}

func (d *Dispatcher) cleanupDeleteOnTerminationVolumesForInstances(ctx context.Context, instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return nil
	}

	terminating := make(map[string]struct{}, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		terminating[instanceID] = struct{}{}
	}

	volumes, err := d.storage.RegisteredResources(types.ResourceTypeVolume)
	if err != nil {
		return fmt.Errorf("retrieving volumes for delete-on-termination cleanup: %w", err)
	}
	if len(volumes) == 0 {
		return nil
	}

	volumeIDs := make([]executor.VolumeID, len(volumes))
	for i, volume := range volumes {
		volumeIDs[i] = executorVolumeID(volume.ID)
	}
	descs, err := d.exe.DescribeVolumes(ctx, executor.DescribeVolumesRequest{VolumeIDs: volumeIDs})
	if err != nil {
		return executorError(err)
	}
	descsByID := make(map[string]executor.VolumeDescription, len(descs))
	for _, desc := range descs {
		descsByID[volumeIDPrefix+string(desc.VolumeID)] = desc
	}

	var result error
	for _, volume := range volumes {
		attrs, err := d.storage.ResourceAttributes(volume.ID)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("retrieving attributes for volume %s: %w", volume.ID, err))
			continue
		}

		rawDeleteOnTermination, found := attrs.Key(attributeNameVolumeDeleteOnTermination)
		if !found || rawDeleteOnTermination == "" {
			continue
		}
		deleteOnTermination, err := strconv.ParseBool(rawDeleteOnTermination)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("parsing delete-on-termination for volume %s: %w", volume.ID, err))
			continue
		}
		if !deleteOnTermination {
			continue
		}

		ownerInstanceID, _ := attrs.Key(attributeNameVolumeDeleteOnTerminationInstanceID)
		if ownerInstanceID == "" {
			continue
		}
		if _, terminatingNow := terminating[ownerInstanceID]; !terminatingNow {
			continue
		}

		desc, found := descsByID[volume.ID]
		if !found {
			continue
		}
		attachedToOwner := false
		for _, attachment := range desc.Attachments {
			if apiInstanceID(attachment.InstanceID) == ownerInstanceID {
				attachedToOwner = true
				break
			}
		}
		if !attachedToOwner {
			continue
		}

		if _, err := d.dispatchDeleteVolume(ctx, &api.DeleteVolumeRequest{VolumeID: volume.ID}); err != nil {
			result = errors.Join(result, fmt.Errorf("deleting delete-on-termination volume %s: %w", volume.ID, err))
		}
	}
	return result
}
