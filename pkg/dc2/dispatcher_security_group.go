package dc2

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	defaultSecurityGroupID          = "sg-00000000000000000"
	defaultSecurityGroupName        = "default"
	defaultSecurityGroupDescription = "default VPC security group"
	defaultSecurityGroupOwnerID     = "000000000000"
	defaultSecurityGroupVPCID       = defaultSubnetVPCID
)

func (d *Dispatcher) dispatchDescribeSecurityGroups(
	_ context.Context,
	req *api.DescribeSecurityGroupsRequest,
) (*api.DescribeSecurityGroupsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	groups := d.listSecurityGroups()

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

func (d *Dispatcher) dispatchCreateSecurityGroup(
	_ context.Context,
	req *api.CreateSecurityGroupRequest,
) (*api.CreateSecurityGroupResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	if err := validateTagSpecifications(req.TagSpecifications, types.ResourceTypeSecurityGroup); err != nil {
		return nil, err
	}

	groupName := strings.TrimSpace(req.GroupName)
	description := strings.TrimSpace(req.Description)
	vpcID := defaultSecurityGroupVPCID
	if req.VPCID != nil && strings.TrimSpace(*req.VPCID) != "" {
		vpcID = strings.TrimSpace(*req.VPCID)
	}

	for _, existing := range d.listSecurityGroups() {
		if !strings.EqualFold(securityGroupStringValue(existing.GroupName), groupName) {
			continue
		}
		if !strings.EqualFold(securityGroupStringValue(existing.VPCID), vpcID) {
			continue
		}
		msg := fmt.Sprintf("The security group '%s' already exists for VPC '%s'.", groupName, vpcID)
		return nil, api.ErrWithCode("InvalidGroup.Duplicate", fmt.Errorf("%s", msg))
	}

	groupID, err := makeID("sg")
	if err != nil {
		return nil, err
	}
	ownerID := defaultSecurityGroupOwnerID
	groupVPCID := vpcID
	group := api.SecurityGroup{
		GroupID:          &groupID,
		GroupName:        &groupName,
		GroupDescription: &description,
		OwnerID:          &ownerID,
		VPCID:            &groupVPCID,
		Tags:             tagSpecsToTags(req.TagSpecifications),
	}
	if err := d.storage.RegisterResource(storage.Resource{Type: types.ResourceTypeSecurityGroup, ID: groupID}); err != nil {
		return nil, fmt.Errorf("registering resource %s: %w", groupID, err)
	}
	if len(group.Tags) > 0 {
		attrs := make([]storage.Attribute, len(group.Tags))
		for i, tag := range group.Tags {
			attrs[i] = storage.Attribute{Key: storage.TagAttributeName(tag.Key), Value: tag.Value}
		}
		if err := d.storage.SetResourceAttributes(groupID, attrs); err != nil {
			return nil, fmt.Errorf("setting resource attributes for %s: %w", groupID, err)
		}
	}
	d.ensureSecurityGroupMap()
	d.securityGroups[groupID] = group
	return &api.CreateSecurityGroupResponse{
		GroupID: &groupID,
	}, nil
}

func (d *Dispatcher) dispatchDeleteSecurityGroup(
	_ context.Context,
	req *api.DeleteSecurityGroupRequest,
) (*api.DeleteSecurityGroupResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	d.ensureSecurityGroupMap()

	groupID, ok := d.resolveSecurityGroupID(req.GroupID, req.GroupName)
	if !ok {
		msg := "The security group does not exist."
		if req.GroupID != nil && strings.TrimSpace(*req.GroupID) != "" {
			msg = fmt.Sprintf("The security group '%s' does not exist.", strings.TrimSpace(*req.GroupID))
		} else if req.GroupName != nil && strings.TrimSpace(*req.GroupName) != "" {
			msg = fmt.Sprintf("The security group '%s' does not exist.", strings.TrimSpace(*req.GroupName))
		}
		return nil, api.ErrWithCode("InvalidGroup.NotFound", fmt.Errorf("%s", msg))
	}

	if groupID == defaultSecurityGroupID {
		msg := fmt.Sprintf("The security group '%s' does not exist.", groupID)
		return nil, api.ErrWithCode("InvalidGroup.NotFound", fmt.Errorf("%s", msg))
	}

	if err := d.storage.RemoveResource(groupID); err != nil {
		var notFound storage.ErrResourceNotFound
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("removing resource %s: %w", groupID, err)
		}
	}
	delete(d.securityGroups, groupID)
	return &api.DeleteSecurityGroupResponse{}, nil
}

func (d *Dispatcher) dispatchAuthorizeSecurityGroupIngress(
	_ context.Context,
	req *api.AuthorizeSecurityGroupIngressRequest,
) (*api.SecurityGroupRuleMutationResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	if _, ok := d.resolveSecurityGroupID(req.GroupID, req.GroupName); !ok {
		msg := "The security group does not exist."
		if req.GroupID != nil && strings.TrimSpace(*req.GroupID) != "" {
			msg = fmt.Sprintf("The security group '%s' does not exist.", strings.TrimSpace(*req.GroupID))
		} else if req.GroupName != nil && strings.TrimSpace(*req.GroupName) != "" {
			msg = fmt.Sprintf("The security group '%s' does not exist.", strings.TrimSpace(*req.GroupName))
		}
		return nil, api.ErrWithCode("InvalidGroup.NotFound", fmt.Errorf("%s", msg))
	}
	return &api.SecurityGroupRuleMutationResponse{Return: true}, nil
}

func (d *Dispatcher) dispatchAuthorizeSecurityGroupEgress(
	_ context.Context,
	req *api.AuthorizeSecurityGroupEgressRequest,
) (*api.SecurityGroupRuleMutationResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	if _, ok := d.resolveSecurityGroupID(req.GroupID, req.GroupName); !ok {
		msg := "The security group does not exist."
		if req.GroupID != nil && strings.TrimSpace(*req.GroupID) != "" {
			msg = fmt.Sprintf("The security group '%s' does not exist.", strings.TrimSpace(*req.GroupID))
		} else if req.GroupName != nil && strings.TrimSpace(*req.GroupName) != "" {
			msg = fmt.Sprintf("The security group '%s' does not exist.", strings.TrimSpace(*req.GroupName))
		}
		return nil, api.ErrWithCode("InvalidGroup.NotFound", fmt.Errorf("%s", msg))
	}
	return &api.SecurityGroupRuleMutationResponse{Return: true}, nil
}

func (d *Dispatcher) listSecurityGroups() []api.SecurityGroup {
	groups := []api.SecurityGroup{d.securityGroupWithStoredTags(defaultSecurityGroup())}
	if len(d.securityGroups) == 0 {
		return groups
	}

	ids := make([]string, 0, len(d.securityGroups))
	for id := range d.securityGroups {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		groups = append(groups, d.securityGroupWithStoredTags(d.securityGroups[id]))
	}
	return groups
}

func (d *Dispatcher) securityGroupWithStoredTags(group api.SecurityGroup) api.SecurityGroup {
	groupID := securityGroupStringValue(group.GroupID)
	if groupID == "" {
		return group
	}
	attrs, err := d.storage.ResourceAttributes(groupID)
	if err != nil {
		return group
	}
	tags := make([]api.Tag, 0, len(attrs))
	for _, attr := range attrs {
		if !attr.IsTag() {
			continue
		}
		tags = append(tags, api.Tag{Key: attr.TagKey(), Value: attr.Value})
	}
	slices.SortFunc(tags, func(a, b api.Tag) int {
		if a.Key != b.Key {
			return strings.Compare(a.Key, b.Key)
		}
		return strings.Compare(a.Value, b.Value)
	})
	group.Tags = tags
	return group
}

func (d *Dispatcher) ensureSecurityGroupMap() {
	if d.securityGroups == nil {
		d.securityGroups = map[string]api.SecurityGroup{}
	}
}

func (d *Dispatcher) resolveSecurityGroupID(groupID *string, groupName *string) (string, bool) {
	d.ensureSecurityGroupMap()

	if groupID != nil {
		id := strings.TrimSpace(*groupID)
		if id != "" {
			if id == defaultSecurityGroupID {
				return id, true
			}
			_, ok := d.securityGroups[id]
			return id, ok
		}
	}

	if groupName != nil {
		name := strings.TrimSpace(*groupName)
		if name != "" {
			for _, group := range d.listSecurityGroups() {
				if strings.EqualFold(securityGroupStringValue(group.GroupName), name) {
					return securityGroupStringValue(group.GroupID), true
				}
			}
			return "", false
		}
	}

	return "", false
}

func defaultSecurityGroup() api.SecurityGroup {
	groupID := defaultSecurityGroupID
	groupName := defaultSecurityGroupName
	groupDescription := defaultSecurityGroupDescription
	ownerID := defaultSecurityGroupOwnerID
	vpcID := defaultSecurityGroupVPCID
	return api.SecurityGroup{
		GroupID:          &groupID,
		GroupName:        &groupName,
		GroupDescription: &groupDescription,
		OwnerID:          &ownerID,
		VPCID:            &vpcID,
	}
}

func tagSpecsToTags(specs []api.TagSpecification) []api.Tag {
	total := 0
	for _, spec := range specs {
		total += len(spec.Tags)
	}
	if total == 0 {
		return nil
	}
	tags := make([]api.Tag, 0, total)
	for _, spec := range specs {
		tags = append(tags, spec.Tags...)
	}
	return tags
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
