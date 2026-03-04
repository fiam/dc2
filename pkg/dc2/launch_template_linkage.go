package dc2

import "github.com/fiam/dc2/pkg/dc2/storage"

const (
	launchTemplateTagKeyID      = "aws:ec2launchtemplate:id"
	launchTemplateTagKeyVersion = "aws:ec2launchtemplate:version"
)

func launchTemplateLinkageTagAttributes(launchTemplateID string, launchTemplateVersion string) []storage.Attribute {
	attrs := make([]storage.Attribute, 0, 2)
	if launchTemplateID != "" {
		attrs = append(attrs, storage.Attribute{
			Key:   storage.TagAttributeName(launchTemplateTagKeyID),
			Value: launchTemplateID,
		})
	}
	if launchTemplateVersion != "" {
		attrs = append(attrs, storage.Attribute{
			Key:   storage.TagAttributeName(launchTemplateTagKeyVersion),
			Value: launchTemplateVersion,
		})
	}
	return attrs
}

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
