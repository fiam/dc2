package dc2

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	launchTemplateIDPrefix = "lt-"

	attributeNameLaunchTemplateName           = "LaunchTemplateName"
	attributeNameLaunchTemplateCreateTime     = "LaunchTemplateCreateTime"
	attributeNameLaunchTemplateDefaultVersion = "LaunchTemplateDefaultVersion"
	attributeNameLaunchTemplateLatestVersion  = "LaunchTemplateLatestVersion"

	// Legacy attributes kept in sync with the current default version.
	attributeNameLaunchTemplateImageID      = "LaunchTemplateDataImageID"
	attributeNameLaunchTemplateInstanceType = "LaunchTemplateDataInstanceType"
	attributeNameLaunchTemplateUserData     = "LaunchTemplateDataUserData"
)

type launchTemplateData struct {
	ID           string
	Name         string
	Version      string
	ImageID      string
	InstanceType string
	UserData     string
}

type launchTemplateMetadata struct {
	ID             string
	Name           string
	CreateTime     *time.Time
	DefaultVersion int64
	LatestVersion  int64
}

type launchTemplateVersionData struct {
	Version            int64
	ImageID            string
	InstanceType       string
	UserData           string
	VersionDescription *string
	CreateTime         *time.Time
}

func (d *Dispatcher) dispatchCreateLaunchTemplate(ctx context.Context, req *api.CreateLaunchTemplateRequest) (*api.CreateLaunchTemplateResponse, error) {
	if req.LaunchTemplateData.ImageID == "" &&
		req.LaunchTemplateData.InstanceType == "" &&
		req.LaunchTemplateData.UserData == "" &&
		len(req.LaunchTemplateData.TagSpecifications) == 0 {
		return nil, api.InvalidParameterValueError("LaunchTemplateData", "<empty>")
	}
	if err := validateLaunchTemplateTagSpecifications(req.LaunchTemplateData.TagSpecifications); err != nil {
		return nil, err
	}
	if _, err := d.findLaunchTemplateByName(ctx, req.LaunchTemplateName); err == nil {
		return nil, api.ErrWithCode("AlreadyExists", fmt.Errorf("launch template %q already exists", req.LaunchTemplateName))
	} else if !errors.As(err, &storage.ErrResourceNotFound{}) {
		return nil, err
	}

	launchTemplateID, err := makeID(launchTemplateIDPrefix)
	if err != nil {
		return nil, err
	}

	if err := d.storage.RegisterResource(storage.Resource{
		Type: types.ResourceTypeLaunchTemplate,
		ID:   launchTemplateID,
	}); err != nil {
		return nil, fmt.Errorf("registering launch template: %w", err)
	}

	now := time.Now().UTC()
	versionData := launchTemplateVersionData{
		Version:      1,
		ImageID:      req.LaunchTemplateData.ImageID,
		InstanceType: req.LaunchTemplateData.InstanceType,
		UserData:     req.LaunchTemplateData.UserData,
		CreateTime:   &now,
	}
	attrs := []storage.Attribute{
		{Key: attributeNameLaunchTemplateName, Value: req.LaunchTemplateName},
		{Key: attributeNameLaunchTemplateCreateTime, Value: now.Format(time.RFC3339Nano)},
		{Key: attributeNameLaunchTemplateDefaultVersion, Value: "1"},
		{Key: attributeNameLaunchTemplateLatestVersion, Value: "1"},
	}
	attrs = append(attrs, launchTemplateVersionAttributes(versionData)...)
	attrs = append(attrs, legacyLaunchTemplateAttributes(versionData)...)
	if err := d.storage.SetResourceAttributes(launchTemplateID, attrs); err != nil {
		return nil, fmt.Errorf("saving launch template attributes: %w", err)
	}

	meta := launchTemplateMetadata{
		ID:             launchTemplateID,
		Name:           req.LaunchTemplateName,
		CreateTime:     &now,
		DefaultVersion: 1,
		LatestVersion:  1,
	}
	launchTemplate := apiLaunchTemplate(meta)
	return &api.CreateLaunchTemplateResponse{
		LaunchTemplate: &launchTemplate,
	}, nil
}

func (d *Dispatcher) dispatchDescribeLaunchTemplates(_ context.Context, req *api.DescribeLaunchTemplatesRequest) (*api.DescribeLaunchTemplatesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	resources, err := d.storage.RegisteredResources(types.ResourceTypeLaunchTemplate)
	if err != nil {
		return nil, fmt.Errorf("retrieving launch templates: %w", err)
	}

	idFilter := make(map[string]struct{}, len(req.LaunchTemplateIDs))
	for _, id := range req.LaunchTemplateIDs {
		idFilter[id] = struct{}{}
	}
	nameFilter := make(map[string]struct{}, len(req.LaunchTemplateNames))
	for _, name := range req.LaunchTemplateNames {
		nameFilter[name] = struct{}{}
	}

	templates := make([]api.LaunchTemplate, 0, len(resources))
	for _, r := range resources {
		if len(idFilter) > 0 {
			if _, ok := idFilter[r.ID]; !ok {
				continue
			}
		}
		meta, err := d.loadLaunchTemplateMetadata(r.ID)
		if err != nil {
			return nil, err
		}
		if len(nameFilter) > 0 {
			if _, ok := nameFilter[meta.Name]; !ok {
				continue
			}
		}
		templates = append(templates, apiLaunchTemplate(*meta))
	}

	templates, nextToken, err := applyNextToken(templates, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, err
	}

	return &api.DescribeLaunchTemplatesResponse{
		LaunchTemplates: templates,
		NextToken:       nextToken,
	}, nil
}

func (d *Dispatcher) dispatchDeleteLaunchTemplate(ctx context.Context, req *api.DeleteLaunchTemplateRequest) (*api.DeleteLaunchTemplateResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	launchTemplateID, err := d.resolveLaunchTemplateReference(ctx, req.LaunchTemplateID, req.LaunchTemplateName)
	if err != nil {
		return nil, err
	}
	meta, err := d.loadLaunchTemplateMetadata(launchTemplateID)
	if err != nil {
		return nil, err
	}
	if err := d.storage.RemoveResource(launchTemplateID); err != nil {
		return nil, fmt.Errorf("deleting launch template: %w", err)
	}
	launchTemplate := apiLaunchTemplate(*meta)
	return &api.DeleteLaunchTemplateResponse{
		LaunchTemplate: &launchTemplate,
	}, nil
}

func (d *Dispatcher) dispatchCreateLaunchTemplateVersion(ctx context.Context, req *api.CreateLaunchTemplateVersionRequest) (*api.CreateLaunchTemplateVersionResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	if err := validateLaunchTemplateTagSpecifications(req.LaunchTemplateData.TagSpecifications); err != nil {
		return nil, err
	}

	launchTemplateID, err := d.resolveLaunchTemplateReference(ctx, req.LaunchTemplateID, req.LaunchTemplateName)
	if err != nil {
		return nil, err
	}
	meta, err := d.loadLaunchTemplateMetadata(launchTemplateID)
	if err != nil {
		return nil, err
	}

	var data launchTemplateVersionData
	if req.SourceVersion != nil && *req.SourceVersion != "" {
		sourceVersion, err := resolveLaunchTemplateVersionSelector(*req.SourceVersion, meta.DefaultVersion, meta.LatestVersion, "SourceVersion")
		if err != nil {
			return nil, err
		}
		sourceData, err := d.loadLaunchTemplateVersionData(launchTemplateID, sourceVersion)
		if err != nil {
			return nil, err
		}
		data.ImageID = sourceData.ImageID
		data.InstanceType = sourceData.InstanceType
		data.UserData = sourceData.UserData
	}
	if req.LaunchTemplateData.ImageID != "" {
		data.ImageID = req.LaunchTemplateData.ImageID
	}
	if req.LaunchTemplateData.InstanceType != "" {
		data.InstanceType = req.LaunchTemplateData.InstanceType
	}
	if req.LaunchTemplateData.UserData != "" {
		data.UserData = req.LaunchTemplateData.UserData
	}

	if req.SourceVersion == nil &&
		req.LaunchTemplateData.ImageID == "" &&
		req.LaunchTemplateData.InstanceType == "" &&
		req.LaunchTemplateData.UserData == "" &&
		len(req.LaunchTemplateData.TagSpecifications) == 0 {
		return nil, api.InvalidParameterValueError("LaunchTemplateData", "<empty>")
	}

	data.Version = meta.LatestVersion + 1
	data.VersionDescription = req.VersionDescription
	now := time.Now().UTC()
	data.CreateTime = &now

	attrs := []storage.Attribute{
		{Key: attributeNameLaunchTemplateLatestVersion, Value: strconv.FormatInt(data.Version, 10)},
	}
	attrs = append(attrs, launchTemplateVersionAttributes(data)...)
	if err := d.storage.SetResourceAttributes(launchTemplateID, attrs); err != nil {
		return nil, fmt.Errorf("saving launch template version attributes: %w", err)
	}

	currentDefault := data.Version == meta.DefaultVersion
	version := d.apiLaunchTemplateVersion(*meta, data, currentDefault)
	return &api.CreateLaunchTemplateVersionResponse{
		LaunchTemplateVersion: &version,
	}, nil
}

func (d *Dispatcher) dispatchDescribeLaunchTemplateVersions(ctx context.Context, req *api.DescribeLaunchTemplateVersionsRequest) (*api.DescribeLaunchTemplateVersionsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	launchTemplateID, err := d.resolveLaunchTemplateReference(ctx, req.LaunchTemplateID, req.LaunchTemplateName)
	if err != nil {
		return nil, err
	}
	meta, err := d.loadLaunchTemplateMetadata(launchTemplateID)
	if err != nil {
		return nil, err
	}

	versionNumbers := make([]int64, 0, meta.LatestVersion)
	if len(req.Versions) > 0 {
		seen := make(map[int64]struct{}, len(req.Versions))
		for _, selector := range req.Versions {
			v, err := resolveLaunchTemplateVersionSelector(selector, meta.DefaultVersion, meta.LatestVersion, "LaunchTemplateVersion")
			if err != nil {
				return nil, err
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			versionNumbers = append(versionNumbers, v)
		}
	} else {
		for v := int64(1); v <= meta.LatestVersion; v++ {
			versionNumbers = append(versionNumbers, v)
		}
	}

	minVersion := int64(1)
	if req.MinVersion != nil && *req.MinVersion != "" {
		v, err := parseNumericLaunchTemplateVersion(*req.MinVersion, "MinVersion")
		if err != nil {
			return nil, err
		}
		minVersion = v
	}
	maxVersion := meta.LatestVersion
	if req.MaxVersion != nil && *req.MaxVersion != "" {
		v, err := parseNumericLaunchTemplateVersion(*req.MaxVersion, "MaxVersion")
		if err != nil {
			return nil, err
		}
		maxVersion = v
	}
	if minVersion > maxVersion {
		return nil, api.InvalidParameterValueError("MinVersion", *req.MinVersion)
	}

	filteredVersions := make([]int64, 0, len(versionNumbers))
	for _, v := range versionNumbers {
		if v >= minVersion && v <= maxVersion {
			filteredVersions = append(filteredVersions, v)
		}
	}
	slices.Sort(filteredVersions)

	versions := make([]api.LaunchTemplateVersion, 0, len(filteredVersions))
	for _, v := range filteredVersions {
		data, err := d.loadLaunchTemplateVersionData(launchTemplateID, v)
		if err != nil {
			return nil, err
		}
		defaultVersion := v == meta.DefaultVersion
		versions = append(versions, d.apiLaunchTemplateVersion(*meta, *data, defaultVersion))
	}

	versions, nextToken, err := applyNextToken(versions, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, err
	}

	return &api.DescribeLaunchTemplateVersionsResponse{
		LaunchTemplateVersions: versions,
		NextToken:              nextToken,
	}, nil
}

func (d *Dispatcher) dispatchModifyLaunchTemplate(ctx context.Context, req *api.ModifyLaunchTemplateRequest) (*api.ModifyLaunchTemplateResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	if req.SetDefaultVersion == nil || *req.SetDefaultVersion == "" {
		return nil, api.InvalidParameterValueError("SetDefaultVersion", "<empty>")
	}

	launchTemplateID, err := d.resolveLaunchTemplateReference(ctx, req.LaunchTemplateID, req.LaunchTemplateName)
	if err != nil {
		return nil, err
	}
	meta, err := d.loadLaunchTemplateMetadata(launchTemplateID)
	if err != nil {
		return nil, err
	}
	defaultVersion, err := resolveLaunchTemplateVersionSelector(*req.SetDefaultVersion, meta.DefaultVersion, meta.LatestVersion, "SetDefaultVersion")
	if err != nil {
		return nil, err
	}
	defaultVersionData, err := d.loadLaunchTemplateVersionData(launchTemplateID, defaultVersion)
	if err != nil {
		return nil, err
	}

	if err := d.storage.RemoveResourceAttributes(launchTemplateID, []storage.Attribute{
		{Key: attributeNameLaunchTemplateImageID},
		{Key: attributeNameLaunchTemplateInstanceType},
		{Key: attributeNameLaunchTemplateUserData},
	}); err != nil {
		return nil, fmt.Errorf("removing legacy launch template attributes: %w", err)
	}
	attrs := []storage.Attribute{
		{Key: attributeNameLaunchTemplateDefaultVersion, Value: strconv.FormatInt(defaultVersion, 10)},
	}
	attrs = append(attrs, legacyLaunchTemplateAttributes(*defaultVersionData)...)
	if err := d.storage.SetResourceAttributes(launchTemplateID, attrs); err != nil {
		return nil, fmt.Errorf("updating launch template attributes: %w", err)
	}

	meta.DefaultVersion = defaultVersion
	launchTemplate := apiLaunchTemplate(*meta)
	return &api.ModifyLaunchTemplateResponse{
		LaunchTemplate: &launchTemplate,
	}, nil
}

func validateLaunchTemplateTagSpecifications(specs []api.TagSpecification) error {
	for i, spec := range specs {
		switch spec.ResourceType {
		case types.ResourceTypeInstance,
			types.ResourceTypeVolume,
			types.ResourceTypeNetworkInterface,
			types.ResourceTypeSpotInstancesRequest:
		default:
			param := fmt.Sprintf("LaunchTemplateData.TagSpecification.%d.ResourceType", i+1)
			return api.InvalidParameterValueError(param, string(spec.ResourceType))
		}
	}
	return nil
}

func (d *Dispatcher) findLaunchTemplate(ctx context.Context, spec *api.AutoScalingLaunchTemplateSpecification) (*launchTemplateData, error) {
	if spec == nil {
		return nil, api.ErrWithCode("ValidationError", fmt.Errorf("LaunchTemplate is required"))
	}

	var (
		templateID      string
		versionSelector string
	)
	if spec.Version != nil {
		versionSelector = *spec.Version
	}
	switch {
	case spec.LaunchTemplateID != nil && *spec.LaunchTemplateID != "":
		templateID = *spec.LaunchTemplateID
	case spec.LaunchTemplateName != nil && *spec.LaunchTemplateName != "":
		data, err := d.findLaunchTemplateByName(ctx, *spec.LaunchTemplateName)
		if err != nil {
			return nil, err
		}
		templateID = data.ID
	default:
		return nil, api.ErrWithCode("ValidationError", fmt.Errorf("either LaunchTemplateId or LaunchTemplateName must be set"))
	}
	if _, err := d.findResource(ctx, types.ResourceTypeLaunchTemplate, templateID); err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("launch template %q was not found", templateID))
		}
		return nil, err
	}
	return d.loadLaunchTemplateData(templateID, versionSelector)
}

func (d *Dispatcher) findLaunchTemplateByID(ctx context.Context, launchTemplateID string) (*launchTemplateData, error) {
	if _, err := d.findResource(ctx, types.ResourceTypeLaunchTemplate, launchTemplateID); err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("launch template %q was not found", launchTemplateID))
		}
		return nil, err
	}
	return d.loadLaunchTemplateData(launchTemplateID, "")
}

func (d *Dispatcher) findLaunchTemplateByName(_ context.Context, launchTemplateName string) (*launchTemplateData, error) {
	templates, err := d.storage.RegisteredResources(types.ResourceTypeLaunchTemplate)
	if err != nil {
		return nil, fmt.Errorf("retrieving launch templates: %w", err)
	}
	for _, r := range templates {
		meta, err := d.loadLaunchTemplateMetadata(r.ID)
		if err != nil {
			return nil, err
		}
		if meta.Name == launchTemplateName {
			return d.loadLaunchTemplateData(r.ID, "")
		}
	}
	return nil, storage.ErrResourceNotFound{ID: launchTemplateName}
}

func (d *Dispatcher) loadLaunchTemplateData(launchTemplateID string, versionSelector string) (*launchTemplateData, error) {
	meta, err := d.loadLaunchTemplateMetadata(launchTemplateID)
	if err != nil {
		return nil, err
	}
	version, err := resolveLaunchTemplateVersionSelector(versionSelector, meta.DefaultVersion, meta.LatestVersion, "Version")
	if err != nil {
		return nil, err
	}
	versionData, err := d.loadLaunchTemplateVersionData(launchTemplateID, version)
	if err != nil {
		return nil, err
	}
	return &launchTemplateData{
		ID:           launchTemplateID,
		Name:         meta.Name,
		Version:      strconv.FormatInt(version, 10),
		ImageID:      versionData.ImageID,
		InstanceType: versionData.InstanceType,
		UserData:     versionData.UserData,
	}, nil
}

func (d *Dispatcher) loadLaunchTemplateMetadata(launchTemplateID string) (*launchTemplateMetadata, error) {
	attrs, err := d.storage.ResourceAttributes(launchTemplateID)
	if err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.ErrWithCode("ValidationError", fmt.Errorf("launch template %q was not found", launchTemplateID))
		}
		return nil, fmt.Errorf("retrieving launch template attributes: %w", err)
	}
	name, _ := attrs.Key(attributeNameLaunchTemplateName)
	if name == "" {
		return nil, fmt.Errorf("launch template %s missing name", launchTemplateID)
	}

	defaultVersion := int64(1)
	if raw, ok := attrs.Key(attributeNameLaunchTemplateDefaultVersion); ok && raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid launch template default version %q: %w", raw, err)
		}
		defaultVersion = v
	}

	latestVersion := int64(1)
	if raw, ok := attrs.Key(attributeNameLaunchTemplateLatestVersion); ok && raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid launch template latest version %q: %w", raw, err)
		}
		latestVersion = v
	}

	var createTime *time.Time
	if raw, ok := attrs.Key(attributeNameLaunchTemplateCreateTime); ok && raw != "" {
		parsed, err := parseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid launch template create time %q: %w", raw, err)
		}
		createTime = &parsed
	}

	return &launchTemplateMetadata{
		ID:             launchTemplateID,
		Name:           name,
		CreateTime:     createTime,
		DefaultVersion: defaultVersion,
		LatestVersion:  latestVersion,
	}, nil
}

func (d *Dispatcher) loadLaunchTemplateVersionData(launchTemplateID string, version int64) (*launchTemplateVersionData, error) {
	attrs, err := d.storage.ResourceAttributes(launchTemplateID)
	if err != nil {
		return nil, fmt.Errorf("retrieving launch template attributes: %w", err)
	}

	imageID, _ := attrs.Key(launchTemplateVersionImageIDAttributeName(version))
	instanceType, _ := attrs.Key(launchTemplateVersionInstanceTypeAttributeName(version))
	userData, _ := attrs.Key(launchTemplateVersionUserDataAttributeName(version))
	if version == 1 {
		if imageID == "" {
			imageID, _ = attrs.Key(attributeNameLaunchTemplateImageID)
		}
		if instanceType == "" {
			instanceType, _ = attrs.Key(attributeNameLaunchTemplateInstanceType)
		}
		if userData == "" {
			userData, _ = attrs.Key(attributeNameLaunchTemplateUserData)
		}
	}

	var createTime *time.Time
	if raw, ok := attrs.Key(launchTemplateVersionCreateTimeAttributeName(version)); ok && raw != "" {
		parsed, err := parseTime(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid launch template version create time %q: %w", raw, err)
		}
		createTime = &parsed
	}
	versionDescription, _ := attrs.Key(launchTemplateVersionDescriptionAttributeName(version))
	var versionDescriptionPtr *string
	if versionDescription != "" {
		versionDescriptionPtr = new(versionDescription)
	}

	return &launchTemplateVersionData{
		Version:            version,
		ImageID:            imageID,
		InstanceType:       instanceType,
		UserData:           userData,
		VersionDescription: versionDescriptionPtr,
		CreateTime:         createTime,
	}, nil
}

func (d *Dispatcher) resolveLaunchTemplateReference(ctx context.Context, launchTemplateID *string, launchTemplateName *string) (string, error) {
	id := strings.TrimSpace(valueOrEmpty(launchTemplateID))
	name := strings.TrimSpace(valueOrEmpty(launchTemplateName))
	hasID := id != ""
	hasName := name != ""

	switch {
	case hasID && hasName:
		return "", api.ErrWithCode("ValidationError", fmt.Errorf("specify either LaunchTemplateId or LaunchTemplateName, but not both"))
	case hasID:
		if _, err := d.findLaunchTemplateByID(ctx, id); err != nil {
			return "", err
		}
		return id, nil
	case hasName:
		lt, err := d.findLaunchTemplateByName(ctx, name)
		if err != nil {
			if errors.As(err, &storage.ErrResourceNotFound{}) {
				return "", api.ErrWithCode("ValidationError", fmt.Errorf("launch template %q was not found", name))
			}
			return "", err
		}
		return lt.ID, nil
	default:
		return "", api.ErrWithCode("ValidationError", fmt.Errorf("either LaunchTemplateId or LaunchTemplateName must be set"))
	}
}

func resolveLaunchTemplateVersionSelector(selector string, defaultVersion, latestVersion int64, paramName string) (int64, error) {
	switch selector {
	case "", "$Default":
		return defaultVersion, nil
	case "$Latest":
		return latestVersion, nil
	default:
		n, err := strconv.ParseInt(selector, 10, 64)
		if err != nil || n <= 0 || n > latestVersion {
			return 0, api.InvalidParameterValueError(paramName, selector)
		}
		return n, nil
	}
}

func parseNumericLaunchTemplateVersion(raw string, paramName string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, api.InvalidParameterValueError(paramName, raw)
	}
	return n, nil
}

func launchTemplateVersionAttributes(data launchTemplateVersionData) []storage.Attribute {
	attrs := []storage.Attribute{}
	if data.ImageID != "" {
		attrs = append(attrs, storage.Attribute{
			Key:   launchTemplateVersionImageIDAttributeName(data.Version),
			Value: data.ImageID,
		})
	}
	if data.InstanceType != "" {
		attrs = append(attrs, storage.Attribute{
			Key:   launchTemplateVersionInstanceTypeAttributeName(data.Version),
			Value: data.InstanceType,
		})
	}
	if data.UserData != "" {
		attrs = append(attrs, storage.Attribute{
			Key:   launchTemplateVersionUserDataAttributeName(data.Version),
			Value: data.UserData,
		})
	}
	if data.VersionDescription != nil {
		attrs = append(attrs, storage.Attribute{
			Key:   launchTemplateVersionDescriptionAttributeName(data.Version),
			Value: *data.VersionDescription,
		})
	}
	if data.CreateTime != nil {
		attrs = append(attrs, storage.Attribute{
			Key:   launchTemplateVersionCreateTimeAttributeName(data.Version),
			Value: data.CreateTime.Format(time.RFC3339Nano),
		})
	}
	return attrs
}

func legacyLaunchTemplateAttributes(data launchTemplateVersionData) []storage.Attribute {
	attrs := []storage.Attribute{}
	if data.ImageID != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameLaunchTemplateImageID, Value: data.ImageID})
	}
	if data.InstanceType != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameLaunchTemplateInstanceType, Value: data.InstanceType})
	}
	if data.UserData != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameLaunchTemplateUserData, Value: data.UserData})
	}
	return attrs
}

func launchTemplateVersionImageIDAttributeName(version int64) string {
	return fmt.Sprintf("LaunchTemplateVersion.%d.ImageID", version)
}

func launchTemplateVersionInstanceTypeAttributeName(version int64) string {
	return fmt.Sprintf("LaunchTemplateVersion.%d.InstanceType", version)
}

func launchTemplateVersionUserDataAttributeName(version int64) string {
	return fmt.Sprintf("LaunchTemplateVersion.%d.UserData", version)
}

func launchTemplateVersionDescriptionAttributeName(version int64) string {
	return fmt.Sprintf("LaunchTemplateVersion.%d.VersionDescription", version)
}

func launchTemplateVersionCreateTimeAttributeName(version int64) string {
	return fmt.Sprintf("LaunchTemplateVersion.%d.CreateTime", version)
}

func apiLaunchTemplate(meta launchTemplateMetadata) api.LaunchTemplate {
	defaultVersionNumber := meta.DefaultVersion
	latestVersionNumber := meta.LatestVersion
	launchTemplateID := meta.ID
	launchTemplateName := meta.Name

	return api.LaunchTemplate{
		CreateTime:           meta.CreateTime,
		DefaultVersionNumber: &defaultVersionNumber,
		LatestVersionNumber:  &latestVersionNumber,
		LaunchTemplateID:     &launchTemplateID,
		LaunchTemplateName:   &launchTemplateName,
	}
}

func (d *Dispatcher) apiLaunchTemplateVersion(meta launchTemplateMetadata, data launchTemplateVersionData, defaultVersion bool) api.LaunchTemplateVersion {
	launchTemplateID := meta.ID
	launchTemplateName := meta.Name
	versionNumber := data.Version
	var imageID *string
	if data.ImageID != "" {
		imageID = new(data.ImageID)
	}
	var instanceType *string
	if data.InstanceType != "" {
		instanceType = new(data.InstanceType)
	}
	var userData *string
	if data.UserData != "" {
		userData = new(data.UserData)
	}

	var launchTemplateData *api.ResponseLaunchTemplateData
	if imageID != nil || instanceType != nil || userData != nil {
		launchTemplateData = &api.ResponseLaunchTemplateData{
			ImageID:      imageID,
			InstanceType: instanceType,
			UserData:     userData,
		}
	}

	return api.LaunchTemplateVersion{
		CreateTime:         data.CreateTime,
		DefaultVersion:     new(defaultVersion),
		LaunchTemplateData: launchTemplateData,
		LaunchTemplateID:   &launchTemplateID,
		LaunchTemplateName: &launchTemplateName,
		VersionDescription: data.VersionDescription,
		VersionNumber:      &versionNumber,
	}
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
