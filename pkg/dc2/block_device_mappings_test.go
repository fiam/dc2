package dc2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
)

func TestValidateBlockDeviceMappings(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }

	valid := []api.RunInstancesBlockDeviceMapping{
		{
			DeviceName: "/dev/sda1",
			EBS: &api.RunInstancesEBSBlockDevice{
				VolumeSize: intPtr(20),
			},
		},
	}

	testCases := []struct {
		name     string
		mappings []api.RunInstancesBlockDeviceMapping
		wantErr  string
	}{
		{name: "valid", mappings: valid},
		{
			name: "missing device name",
			mappings: []api.RunInstancesBlockDeviceMapping{
				{
					EBS: &api.RunInstancesEBSBlockDevice{
						VolumeSize: intPtr(20),
					},
				},
			},
			wantErr: "BlockDeviceMapping.1.DeviceName",
		},
		{
			name: "missing ebs block",
			mappings: []api.RunInstancesBlockDeviceMapping{
				{
					DeviceName: "/dev/sda1",
				},
			},
			wantErr: "BlockDeviceMapping.1.Ebs",
		},
		{
			name: "nil volume size",
			mappings: []api.RunInstancesBlockDeviceMapping{
				{
					DeviceName: "/dev/sda1",
					EBS:        &api.RunInstancesEBSBlockDevice{},
				},
			},
			wantErr: "BlockDeviceMapping.1.Ebs.VolumeSize",
		},
		{
			name: "zero volume size",
			mappings: []api.RunInstancesBlockDeviceMapping{
				{
					DeviceName: "/dev/sda1",
					EBS: &api.RunInstancesEBSBlockDevice{
						VolumeSize: intPtr(0),
					},
				},
			},
			wantErr: "BlockDeviceMapping.1.Ebs.VolumeSize",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateBlockDeviceMappings(tc.mappings, "BlockDeviceMapping")
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestMarshalAndUnmarshalBlockDeviceMappings(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }

	mappings := []api.RunInstancesBlockDeviceMapping{
		{
			DeviceName: "/dev/sda1",
			EBS: &api.RunInstancesEBSBlockDevice{
				DeleteOnTermination: true,
				VolumeSize:          intPtr(64),
			},
		},
	}

	t.Run("marshal empty returns empty string", func(t *testing.T) {
		t.Parallel()

		raw, err := marshalBlockDeviceMappings(nil)
		require.NoError(t, err)
		assert.Equal(t, "", raw)
	})

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		raw, err := marshalBlockDeviceMappings(mappings)
		require.NoError(t, err)
		require.NotEmpty(t, raw)

		got, err := unmarshalBlockDeviceMappings(raw)
		require.NoError(t, err)
		assert.Equal(t, mappings, got)
	})

	t.Run("unmarshal empty returns nil", func(t *testing.T) {
		t.Parallel()

		got, err := unmarshalBlockDeviceMappings("")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("unmarshal invalid json returns wrapped error", func(t *testing.T) {
		t.Parallel()

		_, err := unmarshalBlockDeviceMappings("{not-json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling block device mappings")
	})
}

func TestCloneBlockDeviceMappings(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }
	stringPtr := func(v string) *string { return &v }

	t.Run("empty clone is nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, cloneBlockDeviceMappings(nil))
	})

	t.Run("deep clones nested pointer fields", func(t *testing.T) {
		t.Parallel()

		original := []api.RunInstancesBlockDeviceMapping{
			{
				DeviceName: "/dev/sda1",
				EBS: &api.RunInstancesEBSBlockDevice{
					DeleteOnTermination: true,
					Encrypted:           true,
					Iops:                intPtr(3000),
					KmsKeyID:            stringPtr("kms-key"),
					Throughput:          intPtr(125),
					VolumeSize:          intPtr(20),
				},
			},
			{
				DeviceName: "/dev/sdb",
			},
		}

		cloned := cloneBlockDeviceMappings(original)
		require.Equal(t, original, cloned)
		require.NotNil(t, cloned[0].EBS)
		require.NotNil(t, original[0].EBS)

		assert.NotSame(t, original[0].EBS.Iops, cloned[0].EBS.Iops)
		assert.NotSame(t, original[0].EBS.KmsKeyID, cloned[0].EBS.KmsKeyID)
		assert.NotSame(t, original[0].EBS.Throughput, cloned[0].EBS.Throughput)
		assert.NotSame(t, original[0].EBS.VolumeSize, cloned[0].EBS.VolumeSize)
		assert.Nil(t, cloned[1].EBS)

		*original[0].EBS.Iops = 10
		*original[0].EBS.KmsKeyID = "changed"
		*original[0].EBS.Throughput = 20
		*original[0].EBS.VolumeSize = 1

		assert.Equal(t, 3000, *cloned[0].EBS.Iops)
		assert.Equal(t, "kms-key", *cloned[0].EBS.KmsKeyID)
		assert.Equal(t, 125, *cloned[0].EBS.Throughput)
		assert.Equal(t, 20, *cloned[0].EBS.VolumeSize)
	})
}
