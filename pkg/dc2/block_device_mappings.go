package dc2

import (
	"encoding/json"
	"fmt"

	"github.com/fiam/dc2/pkg/dc2/api"
)

func validateBlockDeviceMappings(mappings []api.RunInstancesBlockDeviceMapping, fieldPrefix string) error {
	for i, mapping := range mappings {
		prefix := fmt.Sprintf("%s.%d", fieldPrefix, i+1)
		if mapping.DeviceName == "" {
			return api.InvalidParameterValueError(prefix+".DeviceName", "<empty>")
		}
		if mapping.EBS == nil {
			return api.InvalidParameterValueError(prefix+".Ebs", "<empty>")
		}
		if mapping.EBS.VolumeSize == nil || *mapping.EBS.VolumeSize <= 0 {
			return api.InvalidParameterValueError(prefix+".Ebs.VolumeSize", "<empty>")
		}
	}
	return nil
}

func marshalBlockDeviceMappings(mappings []api.RunInstancesBlockDeviceMapping) (string, error) {
	if len(mappings) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(mappings)
	if err != nil {
		return "", fmt.Errorf("marshaling block device mappings: %w", err)
	}
	return string(raw), nil
}

func unmarshalBlockDeviceMappings(raw string) ([]api.RunInstancesBlockDeviceMapping, error) {
	if raw == "" {
		return nil, nil
	}
	var mappings []api.RunInstancesBlockDeviceMapping
	if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
		return nil, fmt.Errorf("unmarshaling block device mappings: %w", err)
	}
	return mappings, nil
}

func cloneBlockDeviceMappings(mappings []api.RunInstancesBlockDeviceMapping) []api.RunInstancesBlockDeviceMapping {
	if len(mappings) == 0 {
		return nil
	}
	cloned := make([]api.RunInstancesBlockDeviceMapping, len(mappings))
	for i, mapping := range mappings {
		cloned[i].DeviceName = mapping.DeviceName
		if mapping.EBS == nil {
			continue
		}
		ebs := *mapping.EBS
		if mapping.EBS.Iops != nil {
			value := *mapping.EBS.Iops
			ebs.Iops = &value
		}
		if mapping.EBS.KmsKeyID != nil {
			value := *mapping.EBS.KmsKeyID
			ebs.KmsKeyID = &value
		}
		if mapping.EBS.Throughput != nil {
			value := *mapping.EBS.Throughput
			ebs.Throughput = &value
		}
		if mapping.EBS.VolumeSize != nil {
			value := *mapping.EBS.VolumeSize
			ebs.VolumeSize = &value
		}
		cloned[i].EBS = &ebs
	}
	return cloned
}
