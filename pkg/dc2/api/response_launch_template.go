package api

import "time"

type LaunchTemplate struct {
	CreateTime           *time.Time `xml:"createTime"`
	CreatedBy            *string    `xml:"createdBy"`
	DefaultVersionNumber *int64     `xml:"defaultVersionNumber"`
	LatestVersionNumber  *int64     `xml:"latestVersionNumber"`
	LaunchTemplateId     *string    `xml:"launchTemplateId"`
	LaunchTemplateName   *string    `xml:"launchTemplateName"`
	Tags                 []Tag
}

type ValidationWarning struct {
}

type CreateLaunchTemplateResponse struct {
	LaunchTemplate *LaunchTemplate    `xml:"launchTemplate"`
	Warning        *ValidationWarning `xml:"validationWarning"`
}
