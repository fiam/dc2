package dc2

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	instanceIDPrefix = "i-"

	attributeNameInstanceUserData = "UserData"

	imdsEndpointEnabled  = "enabled"
	imdsEndpointDisabled = "disabled"
	imdsStateApplied     = "applied"
)

func (d *Dispatcher) dispatchRunInstances(ctx context.Context, req *api.RunInstancesRequest) (*api.RunInstancesResponse, error) {
	if err := validateTagSpecifications(req.TagSpecifications, types.ResourceTypeInstance); err != nil {
		return nil, err
	}
	var availabilityZone string
	if req.Placement != nil && req.Placement.AvailabilityZone != "" {
		if err := validateAvailabilityZone(req.Placement.AvailabilityZone, d.opts.Region); err != nil {
			return nil, err
		}
		availabilityZone = req.Placement.AvailabilityZone
	} else {
		availabilityZone = defaultAvailabilityZone(d.opts.Region)
	}
	ids, err := d.exe.CreateInstances(ctx, executor.CreateInstancesRequest{
		ImageID:      req.ImageID,
		InstanceType: req.InstanceType,
		Count:        req.MaxCount,
		UserData:     normalizeUserData(req.UserData),
	})
	if err != nil {
		return nil, executorError(err)
	}

	attrs := []storage.Attribute{
		{
			Key: attributeNameAvailabilityZone, Value: availabilityZone,
		},
	}
	if req.KeyName != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceKeyName, Value: req.KeyName})
	}
	if req.UserData != "" {
		attrs = append(attrs, storage.Attribute{Key: attributeNameInstanceUserData, Value: normalizeUserData(req.UserData)})
	}
	instanceTags := make(map[string]string)
	for _, spec := range req.TagSpecifications {
		for _, tag := range spec.Tags {
			instanceTags[tag.Key] = tag.Value
			attrs = append(attrs, storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value})
		}
	}

	for _, executorID := range ids {
		id := string(instanceIDPrefix + executorID)
		r := storage.Resource{Type: types.ResourceTypeInstance, ID: id}
		if err := d.storage.RegisterResource(r); err != nil {
			return nil, fmt.Errorf("registering instance %s: %w", id, err)
		}
		if len(attrs) > 0 {
			if err := d.storage.SetResourceAttributes(id, attrs); err != nil {
				return nil, fmt.Errorf("storing instance attributes: %w", err)
			}
		}
		if err := setIMDSTags(string(executorID), instanceTags); err != nil {
			return nil, fmt.Errorf("synchronizing IMDS tags for instance %s: %w", id, err)
		}
	}

	if _, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: ids,
	}); err != nil {
		return nil, executorError(err)
	}

	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	instances := make([]api.Instance, len(descriptions))
	for i, desc := range descriptions {
		instances[i], err = d.apiInstance(&desc)
		if err != nil {
			return nil, err
		}
	}
	return &api.RunInstancesResponse{
		InstancesSet: instances,
	}, nil
}

func (d *Dispatcher) dispatchDescribeInstances(ctx context.Context, req *api.DescribeInstancesRequest) (*api.DescribeInstancesResponse, error) {
	instanceIDs, err := d.applyFilters(types.ResourceTypeInstance, req.InstanceIDs, req.Filters)
	if err != nil {
		return nil, err
	}
	ids := executorInstanceIDs(instanceIDs)
	descriptions, err := d.exe.DescribeInstances(ctx, executor.DescribeInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	var reservations []api.Reservation
	if len(descriptions) > 0 {
		instances := make([]api.Instance, len(descriptions))
		for i, desc := range descriptions {
			instances[i], err = d.apiInstance(&desc)
			if err != nil {
				return nil, err
			}
		}
		reservations = append(reservations, api.Reservation{InstancesSet: instances})
	}
	return &api.DescribeInstancesResponse{
		ReservationSet: reservations,
	}, nil
}

func (d *Dispatcher) dispatchStopInstances(ctx context.Context, req *api.StopInstancesRequest) (*api.StopInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	ids := executorInstanceIDs(req.InstanceIDs)
	changes, err := d.exe.StopInstances(ctx, executor.StopInstancesRequest{
		InstanceIDs: ids,
		Force:       req.Force,
	})
	if err != nil {
		return nil, executorError(err)
	}
	return &api.StopInstancesResponse{
		StoppingInstances: apiInstanceChanges(changes),
	}, nil
}

func (d *Dispatcher) dispatchStartInstances(ctx context.Context, req *api.StartInstancesRequest) (*api.StartInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	ids := executorInstanceIDs(req.InstanceIDs)
	changes, err := d.exe.StartInstances(ctx, executor.StartInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	return &api.StartInstancesResponse{
		StartingInstances: apiInstanceChanges(changes),
	}, nil
}

func (d *Dispatcher) dispatchTerminateInstances(ctx context.Context, req *api.TerminateInstancesRequest) (*api.TerminateInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	ids := executorInstanceIDs(req.InstanceIDs)
	changes, err := d.exe.TerminateInstances(ctx, executor.TerminateInstancesRequest{
		InstanceIDs: ids,
	})
	if err != nil {
		return nil, executorError(err)
	}
	for _, instanceID := range req.InstanceIDs {
		containerID := string(executorInstanceID(instanceID))
		if err := setIMDSEnabled(containerID, true); err != nil {
			return nil, fmt.Errorf("resetting IMDS endpoint for instance %s: %w", instanceID, err)
		}
		if err := revokeIMDSTokens(containerID); err != nil {
			return nil, fmt.Errorf("revoking IMDS tokens for instance %s: %w", instanceID, err)
		}
		if err := setIMDSTags(containerID, nil); err != nil {
			return nil, fmt.Errorf("clearing IMDS tags for instance %s: %w", instanceID, err)
		}
	}
	// TODO: remove resources from storage
	return &api.TerminateInstancesResponse{
		TerminatingInstances: apiInstanceChanges(changes),
	}, nil
}

func (d *Dispatcher) dispatchModifyInstanceMetadataOptions(ctx context.Context, req *api.ModifyInstanceMetadataOptionsRequest) (*api.ModifyInstanceMetadataOptionsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	if _, err := d.findInstance(ctx, req.InstanceID); err != nil {
		return nil, err
	}

	httpEndpoint := imdsEndpointEnabled
	if !imdsEnabled(string(executorInstanceID(req.InstanceID))) {
		httpEndpoint = imdsEndpointDisabled
	}
	if req.HttpEndpoint != nil {
		switch strings.ToLower(*req.HttpEndpoint) {
		case imdsEndpointEnabled:
			httpEndpoint = imdsEndpointEnabled
		case imdsEndpointDisabled:
			httpEndpoint = imdsEndpointDisabled
		default:
			return nil, api.InvalidParameterValueError("HttpEndpoint", *req.HttpEndpoint)
		}
	}

	if err := setIMDSEnabled(string(executorInstanceID(req.InstanceID)), httpEndpoint == imdsEndpointEnabled); err != nil {
		return nil, fmt.Errorf("setting IMDS endpoint state for instance %s: %w", req.InstanceID, err)
	}
	instanceID := req.InstanceID
	return &api.ModifyInstanceMetadataOptionsResponse{
		InstanceID: &instanceID,
		InstanceMetadataOptions: &api.InstanceMetadataOptions{
			HttpEndpoint: &httpEndpoint,
			State:        stringPtr(imdsStateApplied),
		},
	}, nil
}

func (d *Dispatcher) findInstance(ctx context.Context, instanceID string) (*storage.Resource, error) {
	instance, err := d.findResource(ctx, types.ResourceTypeInstance, instanceID)
	if err != nil {
		if errors.As(err, &storage.ErrResourceNotFound{}) {
			return nil, api.InvalidParameterValueError("InstanceId", instanceID)
		}
		return nil, err
	}
	return instance, nil
}

func (d *Dispatcher) apiInstance(desc *executor.InstanceDescription) (api.Instance, error) {
	instanceID := instanceIDPrefix + string(desc.InstanceID)
	attrs, err := d.storage.ResourceAttributes(instanceID)
	if err != nil {
		return api.Instance{}, fmt.Errorf("retrieving instance attributes: %w", err)
	}
	keyName, _ := attrs.Key(attributeNameInstanceKeyName)
	var tags []api.Tag
	for _, attr := range attrs {
		if attr.IsTag() {
			tags = append(tags, api.Tag{Key: attr.TagKey(), Value: attr.Value})
		}
	}
	availabilityZone, _ := attrs.Key(attributeNameAvailabilityZone)
	privateDNSName := privateDNSNameFromIP(desc.PrivateIP, d.opts.Region, desc.PrivateDNSName)
	publicDNSName := publicDNSNameFromIP(desc.PublicIP, d.opts.Region, desc.PrivateDNSName)
	return api.Instance{
		InstanceID:       instanceID,
		ImageID:          desc.ImageID,
		InstanceState:    desc.InstanceState,
		PrivateDNSName:   privateDNSName,
		DNSName:          publicDNSName,
		KeyName:          keyName,
		InstanceType:     desc.InstanceType,
		LaunchTime:       desc.LaunchTime,
		Architecture:     desc.Architecture,
		PrivateIPAddress: desc.PrivateIP,
		PublicIPAddress:  desc.PublicIP,
		TagSet:           tags,
		Placement: api.Placement{
			AvailabilityZone: availabilityZone,
		},
	}, nil
}

func privateDNSNameFromIP(privateIP string, region string, fallback string) string {
	ipPart, ok := dashedIPv4ForDNS(privateIP)
	if !ok {
		return fallback
	}
	return fmt.Sprintf("ip-%s.%s.compute.internal", ipPart, region)
}

func publicDNSNameFromIP(publicIP string, region string, fallback string) string {
	ipPart, ok := dashedIPv4ForDNS(publicIP)
	if !ok {
		return fallback
	}
	return fmt.Sprintf("ec2-%s.%s.compute.internal", ipPart, region)
}

func dashedIPv4ForDNS(ip string) (string, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return "", false
	}
	return strings.ReplaceAll(addr.String(), ".", "-"), true
}

func apiInstanceChanges(changes []executor.InstanceStateChange) []api.InstanceStateChange {
	apiChanges := make([]api.InstanceStateChange, len(changes))
	for i, c := range changes {
		apiChanges[i] = api.InstanceStateChange{
			InstanceID:    apiInstanceID(c.InstanceID),
			CurrentState:  c.CurrentState,
			PreviousState: c.PreviousState,
		}
	}
	return apiChanges
}

func executorInstanceIDs(instanceIDs []string) []executor.InstanceID {
	ids := make([]executor.InstanceID, len(instanceIDs))
	for i, id := range instanceIDs {
		ids[i] = executorInstanceID(id)
	}
	return ids
}

func executorInstanceID(instanceID string) executor.InstanceID {
	return executor.InstanceID(instanceID[len(instanceIDPrefix):])
}

func apiInstanceID(instanceID executor.InstanceID) string {
	return instanceIDPrefix + string(instanceID)
}

func defaultAvailabilityZone(region string) string {
	return region + "a"
}

func normalizeUserData(raw string) string {
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	return raw
}

func validateAvailabilityZone(az string, region string) error {
	if strings.HasPrefix(az, region) && len(az) == len(region)+1 {
		azName := az[len(az)-1]
		if azName >= 'a' && azName <= 'z' {
			return nil
		}
	}
	msg := fmt.Sprintf("availability zone %q is not valid for region %q", az, region)
	return api.InvalidParameterValueError("AvailabilityZone", msg)
}
