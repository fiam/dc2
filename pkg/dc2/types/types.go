package types

import (
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type ResourceType = ec2types.ResourceType

const (
	ResourceTypeInstance = ec2types.ResourceTypeInstance
	ResourceTypeVolume   = ec2types.ResourceTypeVolume
)

type VolumeType = ec2types.VolumeType

const (
	VolumeTypeStandard = ec2types.VolumeTypeStandard
	VolumeTypeIo1      = ec2types.VolumeTypeIo1
	VolumeTypeIo2      = ec2types.VolumeTypeIo2
	VolumeTypeGp2      = ec2types.VolumeTypeGp2
	VolumeTypeSc1      = ec2types.VolumeTypeSc1
	VolumeTypeSt1      = ec2types.VolumeTypeSt1
	VolumeTypeGp3      = ec2types.VolumeTypeGp3
)

type VolumeState = ec2types.VolumeState

const (
	VolumeStateCreating  = ec2types.VolumeStateCreating
	VolumeStateAvailable = ec2types.VolumeStateAvailable
	VolumeStateInUse     = ec2types.VolumeStateInUse
	VolumeStateDeleting  = ec2types.VolumeStateDeleting
	VolumeStateDeleted   = ec2types.VolumeStateDeleted
	VolumeStateError     = ec2types.VolumeStateError
)

type VolumeAttachmentState = ec2types.VolumeAttachmentState

const (
	VolumeAttachmentStateAttaching = ec2types.VolumeAttachmentStateAttaching
	VolumeAttachmentStateAttached  = ec2types.VolumeAttachmentStateAttached
	VolumeAttachmentStateDetaching = ec2types.VolumeAttachmentStateDetaching
	VolumeAttachmentStateDetached  = ec2types.VolumeAttachmentStateDetached
	VolumeAttachmentStateBusy      = ec2types.VolumeAttachmentStateBusy
)
