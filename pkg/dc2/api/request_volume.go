package api

import "github.com/fiam/dc2/pkg/dc2/types"

type CreateVolumeRequest struct {
	CommonRequest
	DryRunnableRequest
	AvailabilityZone   string `url:"AvailabilityZone" validate:"required"`
	Encrypted          bool   `url:"Encrypted"`
	Iops               *int
	KmsKeyID           *string `url:"KmsKeyId"`
	MultiAttachEnabled *bool
	OutpostArn         *string
	Size               *int               `url:"Size" validate:"required"`
	SnapshotID         string             `url:"SnapshotId"`
	TagSpecifications  []TagSpecification `url:"TagSpecification"`
	Throughput         *int               `url:"Throughput"`
	VolumeType         types.VolumeType
}

func (r CreateVolumeRequest) Action() Action { return ActionCreateVolume }

type DeleteVolumeRequest struct {
	CommonRequest
	DryRunnableRequest
	VolumeID string `url:"VolumeId" validate:"required"`
}

func (r DeleteVolumeRequest) Action() Action { return ActionDeleteVolume }

type AttachVolumeRequest struct {
	CommonRequest
	DryRunnableRequest
	Device     string `url:"Device" validate:"required"`
	InstanceID string `url:"InstanceId" validate:"required"`
	VolumeID   string `url:"VolumeId" validate:"required"`
}

func (r AttachVolumeRequest) Action() Action { return ActionAttachVolume }

type DetachVolumeRequest struct {
	CommonRequest
	DryRunnableRequest
	Device     string `url:"Device" validate:"required"`
	InstanceID string `url:"InstanceId" validate:"required"`
	VolumeID   string `url:"VolumeId" validate:"required"`
}

func (r DetachVolumeRequest) Action() Action { return ActionDetachVolume }

type DescribeVolumesRequest struct {
	CommonRequest
	DryRunnableRequest
	Filters   []Filter `url:"Filter"`
	VolumeIDs []string `url:"VolumeId"`
}

func (r DescribeVolumesRequest) Action() Action { return ActionDescribeVolumes }
