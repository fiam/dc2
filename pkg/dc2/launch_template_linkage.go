package dc2

import (
	"maps"
	"slices"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	launchTemplateTagKeyID      = "aws:ec2launchtemplate:id"
	launchTemplateTagKeyVersion = "aws:ec2launchtemplate:version"
)

func ensureLaunchTemplateLinkageTags(tags map[string]string, launchTemplateID string, launchTemplateVersion string) map[string]string {
	if launchTemplateID == "" && launchTemplateVersion == "" {
		return tags
	}
	if tags == nil {
		tags = make(map[string]string, 2)
	}
	if launchTemplateID != "" {
		tags[launchTemplateTagKeyID] = launchTemplateID
	}
	if launchTemplateVersion != "" {
		tags[launchTemplateTagKeyVersion] = launchTemplateVersion
	}
	return tags
}

// launchTemplateResourceTags returns the launch template tags for
// resourceType overlaid with overrides, which win on the same key.
func launchTemplateResourceTags(specs []api.TagSpecification, resourceType types.ResourceType, overrides map[string]string) map[string]string {
	tags := make(map[string]string)
	for _, spec := range specs {
		if spec.ResourceType != resourceType {
			continue
		}
		for _, tag := range spec.Tags {
			tags[tag.Key] = tag.Value
		}
	}
	maps.Copy(tags, overrides)
	return tags
}

func tagAttributes(tags map[string]string) []storage.Attribute {
	attrs := make([]storage.Attribute, 0, len(tags))
	for _, key := range slices.Sorted(maps.Keys(tags)) {
		attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(key), Value: tags[key]})
	}
	return attrs
}
