package types

import (
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type ResourceType = ec2types.ResourceType

const (
	ResourceTypeInstance = ec2types.ResourceTypeInstance
)
