package executor

import (
	"context"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
)

type InstanceID string

type CreateInstancesRequest struct {
	ImageID      string
	InstanceType string
	Count        int
	UserData     string
}

type StartInstancesRequest struct {
	InstanceIDs []InstanceID
}

type StopInstancesRequest struct {
	InstanceIDs []InstanceID
	Force       bool
}

type TerminateInstancesRequest struct {
	InstanceIDs []InstanceID
}

type InstanceStateChange struct {
	InstanceID    InstanceID
	CurrentState  api.InstanceState
	PreviousState api.InstanceState
}
type DescribeInstancesRequest struct {
	InstanceIDs []InstanceID
}

type InstanceDescription struct {
	InstanceID     InstanceID
	ImageID        string
	InstanceState  api.InstanceState
	PrivateDNSName string
	PrivateIP      string
	PublicIP       string
	InstanceType   string
	Architecture   string
	LaunchTime     time.Time
}

type InstanceExecutor interface {
	CreateInstances(ctx context.Context, req CreateInstancesRequest) ([]InstanceID, error)
	DescribeInstances(ctx context.Context, req DescribeInstancesRequest) ([]InstanceDescription, error)
	StartInstances(ctx context.Context, req StartInstancesRequest) ([]InstanceStateChange, error)
	StopInstances(ctx context.Context, req StopInstancesRequest) ([]InstanceStateChange, error)
	TerminateInstances(ctx context.Context, req TerminateInstancesRequest) ([]InstanceStateChange, error)
}

type VolumeID string

type CreateVolumeRequest struct {
	// Size is the volume size in bytes
	Size int64
}

type DeleteVolumeRequest struct {
	VolumeID VolumeID
}

type AttachVolumeRequest struct {
	Device     string
	VolumeID   VolumeID
	InstanceID InstanceID
}

type DetachVolumeRequest struct {
	Device     string
	VolumeID   VolumeID
	InstanceID InstanceID
}

type DescribeVolumesRequest struct {
	VolumeIDs []VolumeID
}

type VolumeAttachment struct {
	Device     string
	InstanceID InstanceID
	AttachTime time.Time
}

type VolumeDescription struct {
	VolumeID VolumeID
	// Size is the volume size in bytes
	Size        int64
	Attachments []VolumeAttachment
}

type VolumeExecutor interface {
	CreateVolume(ctx context.Context, req CreateVolumeRequest) (VolumeID, error)
	DeleteVolume(ctx context.Context, req DeleteVolumeRequest) error
	DescribeVolumes(ctx context.Context, req DescribeVolumesRequest) ([]VolumeDescription, error)
	AttachVolume(ctx context.Context, req AttachVolumeRequest) (*VolumeAttachment, error)
	DetachVolume(ctx context.Context, req DetachVolumeRequest) (*VolumeAttachment, error)
}

type Executor interface {
	Close(ctx context.Context) error
	InstanceExecutor
	VolumeExecutor
}
