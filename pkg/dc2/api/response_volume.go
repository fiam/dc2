package api

import (
	"time"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/fiam/dc2/pkg/dc2/types"
)

type CreateVolumeResponse struct {
	Volume
}

type DeleteVolumeResponse struct {
}

type AttachVolumeResponse struct {
	VolumeAttachment
}

type DetachVolumeResponse struct {
	VolumeAttachment
}

type Volume struct {
	// This parameter is not returned by CreateVolume.
	//
	// Information about the volume attachments.
	Attachments []VolumeAttachment `xml:"attachmentSet>item"`

	// The Availability Zone for the volume.
	AvailabilityZone *string `xml:"availabilityZone"`

	// The time stamp when volume creation was initiated.
	CreateTime *time.Time `xml:"createTime"`

	// Indicates whether the volume is encrypted.
	Encrypted *bool `xml:"Encrypted"`

	// This parameter is not returned by CreateVolume.
	//
	// Indicates whether the volume was created using fast snapshot restore.
	FastRestored *bool `xml:"FastRestored"`

	// The number of I/O operations per second (IOPS). For gp3 , io1 , and io2
	// volumes, this represents the number of IOPS that are provisioned for the volume.
	// For gp2 volumes, this represents the baseline performance of the volume and the
	// rate at which the volume accumulates I/O credits for bursting.
	IOPS *int `xml:"Iops"`

	// The Amazon Resource Name (ARN) of the KMS key that was used to protect the
	// volume encryption key for the volume.
	KmsKeyID *string `xml:"KmsKeyId"`

	// Indicates whether Amazon EBS Multi-Attach is enabled.
	MultiAttachEnabled *bool `xml:"multiAttachEnabled"`

	// The entity that manages the volume.
	Operator *ec2types.OperatorResponse

	// The Amazon Resource Name (ARN) of the Outpost.
	OutpostArn *string

	// The size of the volume, in GiBs.
	Size *int `xml:"Size"`

	// The snapshot from which the volume was created, if applicable.
	SnapshotID *string `xml:"SnapshotId"`

	// The volume state.
	State types.VolumeState `xml:"State"`

	// Any tags assigned to the volume.
	Tags []Tag `xml:"tagSet>item"`

	// The throughput that the volume supports, in MiB/s.
	Throughput *int `xml:"Throughput"`

	// The ID of the volume.
	VolumeID *string `xml:"VolumeId"`

	// The volume type.
	VolumeType types.VolumeType `xml:"volumeType"`
}

// Describes volume attachment details.
type VolumeAttachment struct {

	// The ARN of the Amazon ECS or Fargate task to which the volume is attached.
	AssociatedResource *string

	// The time stamp when the attachment initiated.
	AttachTime *time.Time `xml:"attachTime"`

	// Indicates whether the EBS volume is deleted on instance termination.
	DeleteOnTermination *bool `xml:"deleteOnTermination"`

	// The device name.
	//
	// If the volume is attached to a Fargate task, this parameter returns null .
	Device *string `xml:"device"`

	// The ID of the instance.
	//
	// If the volume is attached to a Fargate task, this parameter returns null .
	InstanceID *string `xml:"instanceId"`

	// The service principal of Amazon Web Services service that owns the underlying
	// instance to which the volume is attached.
	//
	// This parameter is returned only for volumes that are attached to Fargate tasks.
	InstanceOwningService *string

	// The attachment state of the volume.
	State types.VolumeAttachmentState `xml:"state"`

	// The ID of the volume.
	VolumeID *string `xml:"volumeId"`
}

type DescribeVolumesResponse struct {
	NextToken *string
	Volumes   []Volume `xml:"volumeSet>item"`
}
