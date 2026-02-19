package api

import "github.com/fiam/dc2/pkg/dc2/types"

type RunInstancesRequest struct {
	CommonRequest
	ImageID               string                             `url:"ImageId" validate:"required"`
	InstanceType          string                             `url:"InstanceType" validate:"required"`
	InstanceMarketOptions *RunInstancesInstanceMarketOptions `url:"InstanceMarketOptions"`
	KeyName               string                             `url:"KeyName"`
	UserData              string                             `url:"UserData"`
	MinCount              int                                `url:"MinCount" validate:"required,gt=0"`
	MaxCount              int                                `url:"MaxCount" validate:"required,gt=0"`
	BlockDeviceMappings   []RunInstancesBlockDeviceMapping   `url:"BlockDeviceMapping"`
	TagSpecifications     []TagSpecification                 `url:"TagSpecification"`
	Placement             *Placement                         `url:"Placement"`
}

func (r RunInstancesRequest) Action() Action { return ActionRunInstances }

type RunInstancesInstanceMarketOptions struct {
	MarketType string `url:"MarketType"`
}

type RunInstancesBlockDeviceMapping struct {
	DeviceName string                      `url:"DeviceName"`
	EBS        *RunInstancesEBSBlockDevice `url:"Ebs"`
}

type RunInstancesEBSBlockDevice struct {
	DeleteOnTermination bool             `url:"DeleteOnTermination"`
	Encrypted           bool             `url:"Encrypted"`
	Iops                *int             `url:"Iops"`
	KmsKeyID            *string          `url:"KmsKeyId"`
	Throughput          *int             `url:"Throughput"`
	VolumeSize          *int             `url:"VolumeSize"`
	VolumeType          types.VolumeType `url:"VolumeType"`
}

type Filter struct {
	Name   *string  `url:"Name"`
	Values []string `url:"Value"`
}

type DescribeInstancesRequest struct {
	CommonRequest
	Filters     []Filter `url:"Filter"`
	InstanceIDs []string `url:"InstanceId"`
}

func (r DescribeInstancesRequest) Action() Action { return ActionDescribeInstances }

type DescribeInstanceStatusRequest struct {
	CommonRequest
	Filters             []Filter `url:"Filter"`
	IncludeAllInstances *bool    `url:"IncludeAllInstances"`
	InstanceIDs         []string `url:"InstanceId"`
	MaxResults          *int     `url:"MaxResults"`
	NextToken           *string  `url:"NextToken"`
}

func (r DescribeInstanceStatusRequest) Action() Action { return ActionDescribeInstanceStatus }

type StopInstancesRequest struct {
	CommonRequest
	DryRunnableRequest
	InstanceIDs []string `url:"InstanceId"`
	Force       bool     `url:"Force"`
}

func (r StopInstancesRequest) Action() Action { return ActionStopInstances }

type StartInstancesRequest struct {
	CommonRequest
	InstanceIDs []string `url:"InstanceId"`
	DryRun      bool     `url:"DryRun"`
}

func (r StartInstancesRequest) Action() Action { return ActionStartInstances }

type TerminateInstancesRequest struct {
	CommonRequest
	DryRunnableRequest
	InstanceIDs []string `url:"InstanceId"`
}

func (r TerminateInstancesRequest) Action() Action { return ActionTerminateInstances }

type ModifyInstanceMetadataOptionsRequest struct {
	CommonRequest
	DryRunnableRequest
	InstanceID   string  `url:"InstanceId" validate:"required"`
	HTTPEndpoint *string `url:"HttpEndpoint"`
}

func (r ModifyInstanceMetadataOptionsRequest) Action() Action {
	return ActionModifyInstanceMetadataOptions
}
