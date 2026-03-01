package dc2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
)

type exitCleanupExecutor struct {
	owned            []executor.InstanceID
	terminateErrByID map[executor.InstanceID]error
	terminateReqs    []executor.TerminateInstancesRequest
}

func (e *exitCleanupExecutor) Close(context.Context) error {
	return nil
}

func (e *exitCleanupExecutor) Disconnect() error {
	return nil
}

func (e *exitCleanupExecutor) ListOwnedInstances(context.Context) ([]executor.InstanceID, error) {
	return append([]executor.InstanceID(nil), e.owned...), nil
}

func (e *exitCleanupExecutor) CreateInstances(context.Context, executor.CreateInstancesRequest) ([]executor.InstanceID, error) {
	return nil, nil
}

func (e *exitCleanupExecutor) DescribeInstances(context.Context, executor.DescribeInstancesRequest) ([]executor.InstanceDescription, error) {
	return nil, nil
}

func (e *exitCleanupExecutor) StartInstances(context.Context, executor.StartInstancesRequest) ([]executor.InstanceStateChange, error) {
	return nil, nil
}

func (e *exitCleanupExecutor) StopInstances(context.Context, executor.StopInstancesRequest) ([]executor.InstanceStateChange, error) {
	return nil, nil
}

func (e *exitCleanupExecutor) TerminateInstances(
	_ context.Context,
	req executor.TerminateInstancesRequest,
) ([]executor.InstanceStateChange, error) {
	e.terminateReqs = append(e.terminateReqs, req)
	if len(req.InstanceIDs) == 1 {
		if err, ok := e.terminateErrByID[req.InstanceIDs[0]]; ok {
			return nil, err
		}
	}
	return nil, nil
}

func (e *exitCleanupExecutor) CreateVolume(context.Context, executor.CreateVolumeRequest) (executor.VolumeID, error) {
	return "", nil
}

func (e *exitCleanupExecutor) DeleteVolume(context.Context, executor.DeleteVolumeRequest) error {
	return nil
}

func (e *exitCleanupExecutor) DescribeVolumes(context.Context, executor.DescribeVolumesRequest) ([]executor.VolumeDescription, error) {
	return nil, nil
}

func (e *exitCleanupExecutor) AttachVolume(context.Context, executor.AttachVolumeRequest) (*executor.VolumeAttachment, error) {
	return nil, assert.AnError
}

func (e *exitCleanupExecutor) DetachVolume(context.Context, executor.DetachVolumeRequest) (*executor.VolumeAttachment, error) {
	return nil, assert.AnError
}

func TestCleanupOwnedInstanceContainersUsesForceTerminate(t *testing.T) {
	t.Parallel()

	exe := &exitCleanupExecutor{
		owned: []executor.InstanceID{"a", "b"},
	}
	dispatch := &Dispatcher{
		exe:  exe,
		imds: &imdsController{},
	}

	err := dispatch.cleanupOwnedInstanceContainers(context.Background())
	require.NoError(t, err)
	require.Len(t, exe.terminateReqs, 2)
	assert.Equal(t, []executor.InstanceID{"a"}, exe.terminateReqs[0].InstanceIDs)
	assert.Equal(t, []executor.InstanceID{"b"}, exe.terminateReqs[1].InstanceIDs)
	assert.True(t, exe.terminateReqs[0].Force)
	assert.True(t, exe.terminateReqs[1].Force)
}

func TestCleanupOwnedInstanceContainersIgnoresNotFound(t *testing.T) {
	t.Parallel()

	exe := &exitCleanupExecutor{
		owned: []executor.InstanceID{"gone", "alive"},
		terminateErrByID: map[executor.InstanceID]error{
			"gone": api.ErrWithCode(api.ErrorCodeInstanceNotFound, assert.AnError),
		},
	}
	dispatch := &Dispatcher{
		exe:  exe,
		imds: &imdsController{},
	}

	err := dispatch.cleanupOwnedInstanceContainers(context.Background())
	require.NoError(t, err)
	require.Len(t, exe.terminateReqs, 2)
}
