package api

import "time"

type LaunchTemplate struct {
	CreateTime           *time.Time `xml:"createTime"`
	CreatedBy            *string    `xml:"createdBy"`
	DefaultVersionNumber *int64     `xml:"defaultVersionNumber"`
	LatestVersionNumber  *int64     `xml:"latestVersionNumber"`
	LaunchTemplateID     *string    `xml:"launchTemplateId"`
	LaunchTemplateName   *string    `xml:"launchTemplateName"`
	Tags                 []Tag      `xml:"tagSet>item"`
}

type ValidationWarning struct {
}

type CreateLaunchTemplateResponse struct {
	LaunchTemplate *LaunchTemplate    `xml:"launchTemplate"`
	Warning        *ValidationWarning `xml:"validationWarning"`
}

type DescribeLaunchTemplatesResponse struct {
	LaunchTemplates []LaunchTemplate `xml:"launchTemplates>item"`
	NextToken       *string          `xml:"nextToken"`
}

type DeleteLaunchTemplateResponse struct {
	LaunchTemplate *LaunchTemplate `xml:"launchTemplate"`
}

type ResponseLaunchTemplateData struct {
	ImageID             *string                                    `xml:"imageId"`
	InstanceType        *string                                    `xml:"instanceType"`
	UserData            *string                                    `xml:"userData"`
	BlockDeviceMappings []ResponseLaunchTemplateBlockDeviceMapping `xml:"blockDeviceMappingSet>item"`
}

type ResponseLaunchTemplateBlockDeviceMapping struct {
	DeviceName *string                               `xml:"deviceName"`
	EBS        *ResponseLaunchTemplateEBSBlockDevice `xml:"ebs"`
}

type ResponseLaunchTemplateEBSBlockDevice struct {
	DeleteOnTermination *bool   `xml:"deleteOnTermination"`
	Encrypted           *bool   `xml:"encrypted"`
	Iops                *int    `xml:"iops"`
	KmsKeyID            *string `xml:"kmsKeyId"`
	Throughput          *int    `xml:"throughput"`
	VolumeSize          *int    `xml:"volumeSize"`
	VolumeType          *string `xml:"volumeType"`
}

type LaunchTemplateVersion struct {
	CreateTime         *time.Time                  `xml:"createTime"`
	CreatedBy          *string                     `xml:"createdBy"`
	DefaultVersion     *bool                       `xml:"defaultVersion"`
	LaunchTemplateData *ResponseLaunchTemplateData `xml:"launchTemplateData"`
	LaunchTemplateID   *string                     `xml:"launchTemplateId"`
	LaunchTemplateName *string                     `xml:"launchTemplateName"`
	VersionDescription *string                     `xml:"versionDescription"`
	VersionNumber      *int64                      `xml:"versionNumber"`
}

type CreateLaunchTemplateVersionResponse struct {
	LaunchTemplateVersion *LaunchTemplateVersion `xml:"launchTemplateVersion"`
	Warning               *ValidationWarning     `xml:"warning"`
}

type DescribeLaunchTemplateVersionsResponse struct {
	LaunchTemplateVersions []LaunchTemplateVersion `xml:"launchTemplateVersionSet>item"`
	NextToken              *string                 `xml:"nextToken"`
}

type ModifyLaunchTemplateResponse struct {
	LaunchTemplate *LaunchTemplate `xml:"launchTemplate"`
}
