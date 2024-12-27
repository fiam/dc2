package executor

import (
	"context"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
)

type CreateInstancesRequest struct {
	ImageID      string
	InstanceType string
	Count        int
}

type StartInstancesRequest struct {
	InstanceIDs []string
}

type StopInstancesRequest struct {
	InstanceIDs []string
	Force       bool
}

type TerminateInstancesRequest struct {
	InstanceIDs []string
}

type InstanceStateChange = api.InstanceStateChange

type DescribeInstancesRequest struct {
	InstanceIDs []string
}

type InstanceDescription struct {
	InstanceID     string
	ImageID        string
	InstanceState  api.InstanceState
	PrivateDNSName string
	InstanceType   string
	Architecture   string
	LaunchTime     time.Time
}

type Executor interface {
	CreateInstances(ctx context.Context, req CreateInstancesRequest) ([]string, error)
	DescribeInstances(ctx context.Context, req DescribeInstancesRequest) ([]InstanceDescription, error)
	StartInstances(ctx context.Context, req StartInstancesRequest) ([]InstanceStateChange, error)
	StopInstances(ctx context.Context, req StopInstancesRequest) ([]InstanceStateChange, error)
	TerminateInstances(ctx context.Context, req TerminateInstancesRequest) ([]InstanceStateChange, error)
}
