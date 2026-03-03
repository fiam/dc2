package dc2

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
)

const (
	defaultSubnetID    = "subnet-00000000000000000"
	defaultSubnetVPCID = "vpc-00000000000000000"
)

func (d *Dispatcher) dispatchDescribeSubnets(
	_ context.Context,
	req *api.DescribeSubnetsRequest,
) (*api.DescribeSubnetsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	availabilityZone := defaultAvailabilityZone(d.opts.Region)
	availabilityZoneID := defaultAvailabilityZoneID(d.opts.Region)
	subnetID := defaultSubnetID
	vpcID := defaultSubnetVPCID
	state := "available"
	cidrBlock := "10.0.0.0/24"
	availableIPAddressCount := 251
	defaultForAZ := true
	mapPublicIPOnLaunch := true

	subnets := []api.Subnet{
		{
			SubnetID:                &subnetID,
			VPCID:                   &vpcID,
			State:                   &state,
			AvailabilityZone:        &availabilityZone,
			AvailabilityZoneID:      &availabilityZoneID,
			CIDRBlock:               &cidrBlock,
			AvailableIPAddressCount: &availableIPAddressCount,
			DefaultForAZ:            &defaultForAZ,
			MapPublicIPOnLaunch:     &mapPublicIPOnLaunch,
		},
	}

	filtered := make([]api.Subnet, 0, len(subnets))
	for _, subnet := range subnets {
		matches, err := subnetMatchesRequest(subnet, req)
		if err != nil {
			return nil, err
		}
		if matches {
			filtered = append(filtered, subnet)
		}
	}

	paged, nextToken, err := applyNextToken(filtered, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, api.InvalidParameterValueError("NextToken", stringValue(req.NextToken))
	}

	return &api.DescribeSubnetsResponse{
		Subnets:   paged,
		NextToken: nextToken,
	}, nil
}

func subnetMatchesRequest(subnet api.Subnet, req *api.DescribeSubnetsRequest) (bool, error) {
	subnetID := subnetStringValue(subnet.SubnetID)
	vpcID := subnetStringValue(subnet.VPCID)
	state := subnetStringValue(subnet.State)
	availabilityZone := subnetStringValue(subnet.AvailabilityZone)
	availabilityZoneID := subnetStringValue(subnet.AvailabilityZoneID)
	cidrBlock := subnetStringValue(subnet.CIDRBlock)
	defaultForAZ := strconv.FormatBool(subnetBoolValue(subnet.DefaultForAZ))

	if len(req.SubnetIDs) > 0 && !slices.Contains(req.SubnetIDs, subnetID) {
		return false, nil
	}

	for _, filter := range req.Filters {
		if filter.Name == nil {
			return false, api.InvalidParameterValueError("Filter.Name", "<missing>")
		}
		if filter.Values == nil {
			return false, api.InvalidParameterValueError("Filter.Values", "<missing>")
		}
		filterName := strings.TrimSpace(strings.ToLower(*filter.Name))
		if filterName == "" {
			return false, api.InvalidParameterValueError("Filter.Name", "<empty>")
		}

		switch {
		case filterName == "subnet-id":
			if !slices.Contains(filter.Values, subnetID) {
				return false, nil
			}
		case filterName == "vpc-id":
			if !slices.Contains(filter.Values, vpcID) {
				return false, nil
			}
		case filterName == "availability-zone":
			if !slices.Contains(filter.Values, availabilityZone) {
				return false, nil
			}
		case filterName == "availability-zone-id":
			if !slices.Contains(filter.Values, availabilityZoneID) {
				return false, nil
			}
		case filterName == "state":
			if !containsFold(filter.Values, state) {
				return false, nil
			}
		case filterName == "default-for-az":
			if !containsFold(filter.Values, defaultForAZ) {
				return false, nil
			}
		case filterName == "cidr-block":
			if !slices.Contains(filter.Values, cidrBlock) {
				return false, nil
			}
		case filterName == "tag-key", strings.HasPrefix(filterName, "tag:"):
			// Subnet tags are not modeled yet.
			return false, nil
		default:
			// Preserve compatibility for callers that send additional AWS filters.
			return false, nil
		}
	}

	return true, nil
}

func subnetStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func subnetBoolValue(value *bool) bool {
	if value == nil {
		return false
	}
	return *value
}
