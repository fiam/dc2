package dc2

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func mergeTestProfileYAML(currentYAML string, patchYAML string) (string, error) {
	current := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.TrimSpace(currentYAML)), &current); err != nil {
		return "", fmt.Errorf("decoding active profile YAML: %w", err)
	}

	patch := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.TrimSpace(patchYAML)), &patch); err != nil {
		return "", fmt.Errorf("decoding patch YAML: %w", err)
	}
	if len(patch) == 0 {
		return "", fmt.Errorf("patch YAML must define at least one field")
	}

	merged := mergeYAMLMaps(current, patch)
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("encoding merged profile YAML: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func mergeYAMLMaps(current map[string]any, patch map[string]any) map[string]any {
	for key, patchValue := range patch {
		if patchValue == nil {
			delete(current, key)
			continue
		}
		currentMap, currentIsMap := current[key].(map[string]any)
		patchMap, patchIsMap := patchValue.(map[string]any)
		if currentIsMap && patchIsMap {
			current[key] = mergeYAMLMaps(currentMap, patchMap)
			continue
		}
		current[key] = patchValue
	}
	return current
}
