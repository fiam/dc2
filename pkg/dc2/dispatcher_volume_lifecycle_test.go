package dc2

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

type launchVolumeExecutor struct {
	*exitCleanupExecutor
}

func (e *launchVolumeExecutor) CreateVolume(context.Context, executor.CreateVolumeRequest) (executor.VolumeID, error) {
	return "0123456789abcdef0", nil
}

func (e *launchVolumeExecutor) DescribeVolumes(_ context.Context, req executor.DescribeVolumesRequest) ([]executor.VolumeDescription, error) {
	out := make([]executor.VolumeDescription, 0, len(req.VolumeIDs))
	for _, id := range req.VolumeIDs {
		out = append(out, executor.VolumeDescription{VolumeID: id})
	}
	return out, nil
}

func (e *launchVolumeExecutor) AttachVolume(_ context.Context, req executor.AttachVolumeRequest) (*executor.VolumeAttachment, error) {
	return &executor.VolumeAttachment{Device: req.Device, InstanceID: req.InstanceID, AttachTime: time.Now()}, nil
}

func TestAttachInstanceBlockDeviceMappingsTagsVolumes(t *testing.T) {
	t.Parallel()

	dispatch := &Dispatcher{
		exe:     &launchVolumeExecutor{exitCleanupExecutor: &exitCleanupExecutor{}},
		storage: storage.NewMemoryStorage(),
	}
	instanceID := executor.InstanceID("0123456789abcdef1")
	require.NoError(t, dispatch.storage.RegisterResource(storage.Resource{Type: types.ResourceTypeInstance, ID: apiInstanceID(instanceID)}))
	require.NoError(t, dispatch.storage.SetResourceAttributes(apiInstanceID(instanceID), []storage.Attribute{
		{Key: attributeNameAvailabilityZone, Value: "us-east-1a"},
	}))
	size := 1
	mappings := []api.RunInstancesBlockDeviceMapping{
		{DeviceName: "/dev/sdf", EBS: &api.RunInstancesEBSBlockDevice{VolumeSize: &size}},
	}

	err := dispatch.attachInstanceBlockDeviceMappings(
		context.Background(),
		[]executor.InstanceID{instanceID},
		"us-east-1a",
		mappings,
		map[string]string{"backup": "daily", "team": "compute"},
	)
	require.NoError(t, err)

	attrs, err := dispatch.storage.ResourceAttributes(volumeIDPrefix + "0123456789abcdef0")
	require.NoError(t, err)
	tags := map[string]string{}
	for _, attr := range attrs {
		if attr.IsTag() {
			tags[attr.TagKey()] = attr.Value
		}
	}
	assert.Equal(t, map[string]string{"backup": "daily", "team": "compute"}, tags)
}

func TestLaunchTemplateResourceTags(t *testing.T) {
	t.Parallel()

	specs := []api.TagSpecification{
		{ResourceType: types.ResourceTypeInstance, Tags: []api.Tag{{Key: "Name", Value: "launch-template"}, {Key: "team", Value: "compute"}}},
		{ResourceType: types.ResourceTypeVolume, Tags: []api.Tag{{Key: "backup", Value: "daily"}}},
	}

	assert.Equal(t,
		map[string]string{"Name": "request", "team": "compute"},
		launchTemplateResourceTags(specs, types.ResourceTypeInstance, map[string]string{"Name": "request"}),
	)
	assert.Equal(t,
		map[string]string{"backup": "daily"},
		launchTemplateResourceTags(specs, types.ResourceTypeVolume, nil),
	)
	assert.Empty(t, launchTemplateResourceTags(nil, types.ResourceTypeInstance, nil))
}
