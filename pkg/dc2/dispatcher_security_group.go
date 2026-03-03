package dc2

import (
	"context"
	"slices"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
)

const (
	defaultSecurityGroupID          = "sg-00000000000000000"
	defaultSecurityGroupName        = "default"
	defaultSecurityGroupDescription = "default VPC security group"
	defaultSecurityGroupOwnerID     = "000000000000"
)

func (d *Dispatcher) dispatchDescribeSecurityGroups(
	_ context.Context,
	req *api.DescribeSecurityGroupsRequest,
) (*api.DescribeSecurityGroupsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	groupID := defaultSecurityGroupID
	groupName := defaultSecurityGroupName
	groupDescription := defaultSecurityGroupDescription
	ownerID := defaultSecurityGroupOwnerID
	groups := []api.SecurityGroup{
		{
			GroupID:          &groupID,
			GroupName:        &groupName,
			GroupDescription: &groupDescription,
			OwnerID:          &ownerID,
		},
	}

	filtered := make([]api.SecurityGroup, 0, len(groups))
	for _, group := range groups {
		matches, err := securityGroupMatchesRequest(group, req)
		if err != nil {
			return nil, err
		}
		if matches {
			filtered = append(filtered, group)
		}
	}

	return &api.DescribeSecurityGroupsResponse{
		SecurityGroups: filtered,
	}, nil
}

func securityGroupMatchesRequest(group api.SecurityGroup, req *api.DescribeSecurityGroupsRequest) (bool, error) {
	groupID := securityGroupStringValue(group.GroupID)
	groupName := securityGroupStringValue(group.GroupName)
	groupDescription := securityGroupStringValue(group.GroupDescription)
	ownerID := securityGroupStringValue(group.OwnerID)
	vpcID := securityGroupStringValue(group.VPCID)

	if len(req.GroupIDs) > 0 && !slices.Contains(req.GroupIDs, groupID) {
		return false, nil
	}
	if len(req.GroupNames) > 0 && !slices.Contains(req.GroupNames, groupName) {
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
		case filterName == "group-id":
			if !slices.Contains(filter.Values, groupID) {
				return false, nil
			}
		case filterName == "group-name":
			if !slices.Contains(filter.Values, groupName) {
				return false, nil
			}
		case filterName == "description":
			if !slices.Contains(filter.Values, groupDescription) {
				return false, nil
			}
		case filterName == "owner-id":
			if !slices.Contains(filter.Values, ownerID) {
				return false, nil
			}
		case filterName == "vpc-id":
			if !slices.Contains(filter.Values, vpcID) {
				return false, nil
			}
		case filterName == "tag-key", strings.HasPrefix(filterName, "tag:"):
			// Security group tags are not modeled yet.
			return false, nil
		default:
			// Preserve compatibility for callers that send additional AWS filters.
			return false, nil
		}
	}

	return true, nil
}

func securityGroupStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
