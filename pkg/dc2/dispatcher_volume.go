package dc2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	attributeNameVolumeType                          = "VolumeType"
	attributeNameEncrypted                           = "Encrypted"
	attributeNameKMSKeyID                            = "KMSKeyID"
	attributeNameIOPS                                = "IOPS"
	attributeNameThroughput                          = "Throughput"
	attributeNameSnapshotID                          = "SnapshotID"
	attributeNameVolumeDeleteOnTermination           = "DeleteOnTermination"
	attributeNameVolumeDeleteOnTerminationInstanceID = "DeleteOnTerminationInstanceID"

	volumeIDPrefix = "vol-"

	bytesPerGigaByte = 1024 * 1024 * 1024
)

func (d *Dispatcher) dispatchCreateVolume(ctx context.Context, req *api.CreateVolumeRequest) (*api.CreateVolumeResponse, error) {
	if err := validateTagSpecifications(req.TagSpecifications, types.ResourceTypeVolume); err != nil {
		return nil, err
	}
	if req.Size == nil || *req.Size == 0 {
		return nil, api.InvalidParameterValueError("Size", fmt.Sprintf("%v", req.Size))
	}

	if req.DryRun {
		return nil, api.DryRunError()
	}

	createdTime := time.Now().UTC()

	IOPS := 0
	if req.Iops != nil {
		IOPS = *req.Iops
	}

	throughput := 0
	if req.Throughput != nil {
		throughput = *req.Throughput
	}

	sizeInBytesFromGB := int64(*req.Size) * bytesPerGigaByte
	volID, err := d.exe.CreateVolume(ctx, executor.CreateVolumeRequest{Size: sizeInBytesFromGB})
	if err != nil {
		return nil, executorError(err)
	}

	attrs := []storage.Attribute{
		{Key: attributeNameAvailabilityZone, Value: req.AvailabilityZone},
		{Key: attributeNameCreateTime, Value: createdTime.Format(time.RFC3339Nano)},
		{Key: attributeNameEncrypted, Value: strconv.FormatBool(req.Encrypted)},
		{Key: attributeNameVolumeType, Value: string(req.VolumeType)},
		{Key: attributeNameIOPS, Value: strconv.Itoa(IOPS)},
		{Key: attributeNameThroughput, Value: strconv.Itoa(throughput)},
	}
	if req.KmsKeyID != nil && *req.KmsKeyID != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameKMSKeyID, Value: *req.KmsKeyID})
	}
	for _, spec := range req.TagSpecifications {
		for _, tag := range spec.Tags {
			attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value})
		}
	}

	id := volumeIDPrefix + string(volID)

	r := storage.Resource{Type: types.ResourceTypeVolume, ID: id}
	if err := d.storage.RegisterResource(r); err != nil {
		return nil, fmt.Errorf("registering volume %s: %w", id, err)
	}
	if len(attrs) > 0 {
		if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
			return nil, fmt.Errorf("storing instance attributes: %w", err)
		}
	}
	api.Logger(ctx).Info(
		"created volume",
		slog.String("volume_id", id),
		slog.String("availability_zone", req.AvailabilityZone),
		slog.Int("size_gib", *req.Size),
		slog.String("volume_type", string(req.VolumeType)),
	)
	vol, err := d.describeVolume(ctx, id)
	if err != nil {
		return nil, err
	}

	return &api.CreateVolumeResponse{Volume: vol}, nil
}

func (d *Dispatcher) dispatchDeleteVolume(ctx context.Context, req *api.DeleteVolumeRequest) (*api.DeleteVolumeResponse, error) {
	vol, err := d.findVolume(ctx, req.VolumeID)
	if err != nil {
		return nil, err
	}

	if err := d.exe.DeleteVolume(ctx, executor.DeleteVolumeRequest{VolumeID: executorVolumeID(vol.ID)}); err != nil {
		return nil, executorError(err)
	}

	if err := d.storage.RemoveResource(vol.ID); err != nil {
		return nil, fmt.Errorf("deleting volume from storage: %w", err)
	}
	api.Logger(ctx).Info("deleted volume", slog.String("volume_id", vol.ID))
	return &api.DeleteVolumeResponse{}, nil
}

func (d *Dispatcher) dispatchAttachVolume(ctx context.Context, req *api.AttachVolumeRequest) (*api.AttachVolumeResponse, error) {
	vol, err := d.findVolume(ctx, req.VolumeID)
	if err != nil {
		return nil, err
	}

	instance, err := d.findInstance(ctx, req.InstanceID)
	if err != nil {
		return nil, err
	}

	volumeAttributes, err := d.storage.ResourceAttributes(vol.ID)
	if err != nil {
		return nil, fmt.Errorf("retrieving volume attributes: %w", err)
	}

	instanceAttributes, err := d.storage.ResourceAttributes(instance.ID)
	if err != nil {
		return nil, fmt.Errorf("retrieving instance attributes: %w", err)
	}

	volumeAZ, volumeAZFound := volumeAttributes.Key(attributeNameAvailabilityZone)
	if !volumeAZFound {
		return nil, fmt.Errorf("volume %s missing availability zone", vol.ID)
	}

	instanceAZ, instanceAZFound := instanceAttributes.Key(attributeNameAvailabilityZone)
	if !instanceAZFound {
		return nil, fmt.Errorf("instance %s missing availability zone", instance.ID)
	}

	if volumeAZ != instanceAZ {
		return nil, api.InvalidParameterValueError("AvailabilityZone", fmt.Sprintf("volume %s and instance %s are in different availability zones", vol.ID, instance.ID))
	}

	if req.DryRun {
		return nil, api.DryRunError()
	}

	attachment, err := d.exe.AttachVolume(ctx, executor.AttachVolumeRequest{
		Device:     req.Device,
		VolumeID:   executorVolumeID(vol.ID),
		InstanceID: executorInstanceID(instance.ID),
	})
	if err != nil {
		return nil, executorError(err)
	}

	api.Logger(ctx).Debug("attached volume", slog.String("volume_id", vol.ID), slog.String("instance_id", instance.ID))
	deleteOnTermination := false
	return &api.AttachVolumeResponse{
		VolumeAttachment: api.VolumeAttachment{
			AttachTime:          &attachment.AttachTime,
			Device:              &req.Device,
			InstanceID:          &req.InstanceID,
			VolumeID:            &req.VolumeID,
			State:               types.VolumeAttachmentStateAttached,
			DeleteOnTermination: &deleteOnTermination,
		},
	}, nil
}

func (d *Dispatcher) dispatchDetachVolume(ctx context.Context, req *api.DetachVolumeRequest) (*api.DetachVolumeResponse, error) {
	vol, err := d.findVolume(ctx, req.VolumeID)
	if err != nil {
		return nil, err
	}

	instance, err := d.findInstance(ctx, req.InstanceID)
	if err != nil {
		return nil, err
	}

	if req.DryRun {
		return nil, api.DryRunError()
	}

	attachment, err := d.exe.DetachVolume(ctx, executor.DetachVolumeRequest{
		Device:     req.Device,
		VolumeID:   executorVolumeID(vol.ID),
		InstanceID: executorInstanceID(instance.ID),
	})
	if err != nil {
		return nil, executorError(err)
	}

	api.Logger(ctx).Debug("detached volume", slog.String("volume_id", vol.ID), slog.String("instance_id", instance.ID))
	deleteOnTermination := false
	return &api.DetachVolumeResponse{
		VolumeAttachment: api.VolumeAttachment{
			AttachTime:          &attachment.AttachTime,
			Device:              &req.Device,
			InstanceID:          &req.InstanceID,
			VolumeID:            &req.VolumeID,
			State:               types.VolumeAttachmentStateAttached,
			DeleteOnTermination: &deleteOnTermination,
		},
	}, nil
}

func (d *Dispatcher) dispatchDescribeVolumes(ctx context.Context, req *api.DescribeVolumesRequest) (*api.DescribeVolumesResponse, error) {
	volumeIDs, err := d.applyFilters(types.ResourceTypeVolume, req.VolumeIDs, req.Filters)
	if err != nil {
		return nil, err
	}

	if req.DryRun {
		return nil, api.DryRunError()
	}

	volumes := make([]api.Volume, 0, len(volumeIDs))
	for _, id := range volumeIDs {
		volume, err := d.describeVolume(ctx, id)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, volume)
	}

	volumes, nextToken, err := applyNextToken(volumes, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, err
	}

	return &api.DescribeVolumesResponse{
		Volumes:   volumes,
		NextToken: nextToken,
	}, nil
}

func (d *Dispatcher) findVolume(ctx context.Context, volumeID string) (*storage.Resource, error) {
	volume, err := d.findResource(ctx, types.ResourceTypeVolume, volumeID)
	if err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.InvalidParameterValueError("VolumeId", volumeID)
		}
		return nil, err
	}
	return volume, nil
}

func (d *Dispatcher) describeVolume(ctx context.Context, volumeID string) (api.Volume, error) {
	v, err := d.findVolume(ctx, volumeID)
	if err != nil {
		return api.Volume{}, fmt.Errorf("invalid volume ID: %w", err)
	}
	attrs, err := d.storage.ResourceAttributes(v.ID)
	if err != nil {
		return api.Volume{}, fmt.Errorf("volume ID without attributes: %w", err)
	}

	// TODO: improve this
	req := executor.DescribeVolumesRequest{
		VolumeIDs: []executor.VolumeID{executorVolumeID(volumeID)},
	}
	descriptions, err := d.exe.DescribeVolumes(ctx, req)
	if err != nil {
		return api.Volume{}, executorError(err)
	}
	desc := descriptions[0]

	deleteOnTermination := false
	if rawDeleteOnTermination, found := attrs.Key(attributeNameVolumeDeleteOnTermination); found && rawDeleteOnTermination != "" {
		parsedDeleteOnTermination, err := strconv.ParseBool(rawDeleteOnTermination)
		if err != nil {
			return api.Volume{}, fmt.Errorf("invalid volume delete on termination attribute: %w", err)
		}
		deleteOnTermination = parsedDeleteOnTermination
	}
	deleteOnTerminationInstanceID, _ := attrs.Key(attributeNameVolumeDeleteOnTerminationInstanceID)

	attachments := make([]api.VolumeAttachment, len(desc.Attachments))
	for i, a := range desc.Attachments {
		instanceID := apiInstanceID(a.InstanceID)
		attachmentDeleteOnTermination := deleteOnTermination && instanceID == deleteOnTerminationInstanceID
		attachments[i] = api.VolumeAttachment{
			AttachTime:          &a.AttachTime,
			Device:              &a.Device,
			InstanceID:          &instanceID,
			State:               types.VolumeAttachmentStateAttached,
			DeleteOnTermination: &attachmentDeleteOnTermination,
		}
	}

	availabilityZone, _ := attrs.Key(attributeNameAvailabilityZone)
	createTime, err := parseAttr(attrs, attributeNameCreateTime, parseTime)
	if err != nil {
		return api.Volume{}, fmt.Errorf("invalid volume creation time: %w", err)
	}
	encrypted, err := parseAttr(attrs, attributeNameEncrypted, strconv.ParseBool)
	if err != nil {
		return api.Volume{}, fmt.Errorf("invalid volume encrypted attribute: %w", err)
	}

	fastRestored := false

	iops, err := parseAttr(attrs, attributeNameIOPS, strconv.Atoi)
	if err != nil {
		return api.Volume{}, fmt.Errorf("invalid volume IOPS attribute: %w", err)
	}

	var kmsKeyID *string
	if kmsKeyIDStr, found := attrs.Key(attributeNameKMSKeyID); found {
		kmsKeyID = &kmsKeyIDStr
	}

	multiattachEnabled := false

	size := int(desc.Size / bytesPerGigaByte)

	var snapshotID *string
	if snapshotIDStr, found := attrs.Key(attributeNameSnapshotID); found {
		snapshotID = &snapshotIDStr
	}

	var tags []api.Tag
	for _, attr := range attrs {
		if attr.IsTag() {
			tags = append(tags, api.Tag{Key: attr.TagKey(), Value: attr.Value})
		}
	}

	throughput, err := parseAttr(attrs, attributeNameThroughput, strconv.Atoi)
	if err != nil {
		return api.Volume{}, fmt.Errorf("invalid volume throughput attribute: %w", err)
	}

	volumeType, found := attrs.Key(attributeNameVolumeType)
	if !found {
		return api.Volume{}, errors.New("missing volume type attribute")
	}

	return api.Volume{
		Attachments:        attachments,
		AvailabilityZone:   &availabilityZone,
		CreateTime:         &createTime,
		Encrypted:          &encrypted,
		FastRestored:       &fastRestored,
		IOPS:               &iops,
		KmsKeyID:           kmsKeyID,
		MultiAttachEnabled: &multiattachEnabled,
		Size:               &size,
		SnapshotID:         snapshotID,
		State:              types.VolumeStateAvailable,
		Tags:               tags,
		Throughput:         &throughput,
		VolumeID:           &volumeID,
		VolumeType:         types.VolumeType(volumeType),
	}, nil
}

func executorVolumeID(volID string) executor.VolumeID {
	return executor.VolumeID(volID[len(volumeIDPrefix):])
}
