package dc2

import (
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/fiam/dc2/pkg/dc2/api"
)

func (d *Dispatcher) dispatchDescribeInstanceTypes(req *api.DescribeInstanceTypesRequest) (*api.DescribeInstanceTypesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	instanceTypes := d.catalogInstanceTypes(req.InstanceTypes)
	for _, filter := range req.Filters {
		name, err := normalizedFilter(filter)
		if err != nil {
			return nil, err
		}
		switch name {
		case "instance-type":
			instanceTypes = filterValues(instanceTypes, filter.Values)
		default:
			return nil, api.InvalidParameterValueError("Filter.Name", name)
		}
	}

	items := make([]map[string]any, 0, len(instanceTypes))
	for _, instanceType := range instanceTypes {
		item := maps.Clone(d.instanceTypeCatalog.InstanceTypes[instanceType])
		if item == nil {
			item = map[string]any{}
		}
		item["InstanceType"] = instanceType
		items = append(items, item)
	}

	paged, nextToken, err := applyNextToken(items, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, api.InvalidParameterValueError("NextToken", stringValue(req.NextToken))
	}

	return &api.DescribeInstanceTypesResponse{
		InstanceTypes: paged,
		NextToken:     nextToken,
	}, nil
}

func (d *Dispatcher) dispatchDescribeInstanceTypeOfferings(req *api.DescribeInstanceTypeOfferingsRequest) (*api.DescribeInstanceTypeOfferingsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	instanceTypes := d.catalogInstanceTypes(nil)
	locations := []string{}
	locationTypeFilters := []string{}
	for _, filter := range req.Filters {
		name, err := normalizedFilter(filter)
		if err != nil {
			return nil, err
		}
		switch name {
		case "instance-type":
			instanceTypes = filterValues(instanceTypes, filter.Values)
		case "location":
			locations = append(locations, filter.Values...)
		case "location-type":
			locationTypeFilters = append(locationTypeFilters, filter.Values...)
		default:
			return nil, api.InvalidParameterValueError("Filter.Name", name)
		}
	}

	locationType := strings.TrimSpace(req.LocationType)
	if locationType == "" && len(locationTypeFilters) > 0 {
		locationType = strings.TrimSpace(locationTypeFilters[0])
	}
	if locationType == "" {
		locationType = "region"
	}
	if !isValidLocationType(locationType) {
		return nil, api.InvalidParameterValueError("LocationType", locationType)
	}
	if len(locationTypeFilters) > 0 && !containsFold(locationTypeFilters, locationType) {
		instanceTypes = nil
	}

	if len(locations) == 0 {
		locations = []string{d.opts.Region}
	}
	locations = dedupSortedStrings(locations)

	// The catalog is intentionally sourced from us-east-1 only. In the fake API,
	// treat every known instance type as available in every requested location.
	offerings := make([]api.InstanceTypeOffering, 0, len(instanceTypes)*len(locations))
	for _, location := range locations {
		for _, instanceType := range instanceTypes {
			offerings = append(offerings, api.InstanceTypeOffering{
				InstanceType: instanceType,
				LocationType: locationType,
				Location:     location,
			})
		}
	}

	slices.SortFunc(offerings, func(a, b api.InstanceTypeOffering) int {
		if a.LocationType != b.LocationType {
			return strings.Compare(a.LocationType, b.LocationType)
		}
		if a.Location != b.Location {
			return strings.Compare(a.Location, b.Location)
		}
		return strings.Compare(a.InstanceType, b.InstanceType)
	})

	paged, nextToken, err := applyNextToken(offerings, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, api.InvalidParameterValueError("NextToken", stringValue(req.NextToken))
	}

	return &api.DescribeInstanceTypeOfferingsResponse{
		InstanceTypeOfferings: paged,
		NextToken:             nextToken,
	}, nil
}

func (d *Dispatcher) dispatchGetInstanceTypesFromInstanceRequirements(req *api.GetInstanceTypesFromInstanceRequirementsRequest) (*api.GetInstanceTypesFromInstanceRequirementsResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}

	instanceTypes := d.catalogInstanceTypes(nil)
	matches := make([]api.InstanceTypeInfoFromInstanceRequirements, 0, len(instanceTypes))
	for _, instanceType := range instanceTypes {
		data := d.instanceTypeCatalog.InstanceTypes[instanceType]
		if !matchesInstanceTypeFromRequirements(instanceType, data, req) {
			continue
		}
		matches = append(matches, api.InstanceTypeInfoFromInstanceRequirements{InstanceType: instanceType})
	}

	paged, nextToken, err := applyNextToken(matches, req.NextToken, req.MaxResults)
	if err != nil {
		return nil, api.InvalidParameterValueError("NextToken", stringValue(req.NextToken))
	}
	return &api.GetInstanceTypesFromInstanceRequirementsResponse{
		InstanceTypes: paged,
		NextToken:     nextToken,
	}, nil
}

func (d *Dispatcher) catalogInstanceTypes(requested []string) []string {
	if len(requested) == 0 {
		out := make([]string, 0, len(d.instanceTypeCatalog.InstanceTypes))
		for instanceType := range d.instanceTypeCatalog.InstanceTypes {
			out = append(out, instanceType)
		}
		slices.Sort(out)
		return out
	}

	seen := make(map[string]struct{}, len(requested))
	out := make([]string, 0, len(requested))
	for _, instanceType := range requested {
		instanceType = strings.TrimSpace(instanceType)
		if instanceType == "" {
			continue
		}
		if _, ok := d.instanceTypeCatalog.InstanceTypes[instanceType]; !ok {
			continue
		}
		if _, dup := seen[instanceType]; dup {
			continue
		}
		seen[instanceType] = struct{}{}
		out = append(out, instanceType)
	}
	slices.Sort(out)
	return out
}

func normalizedFilter(filter api.Filter) (string, error) {
	if filter.Name == nil {
		return "", api.InvalidParameterValueError("Filter.Name", "<missing>")
	}
	if filter.Values == nil {
		return "", api.InvalidParameterValueError("Filter.Values", "<missing>")
	}
	name := strings.TrimSpace(*filter.Name)
	if name == "" {
		return "", api.InvalidParameterValueError("Filter.Name", "<empty>")
	}
	return strings.ToLower(name), nil
}

func filterValues(values []string, allowed []string) []string {
	if len(values) == 0 {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := allowedSet[value]; ok {
			out = append(out, value)
		}
	}
	return dedupSortedStrings(out)
}

func dedupSortedStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func isValidLocationType(locationType string) bool {
	switch strings.ToLower(strings.TrimSpace(locationType)) {
	case "region", "availability-zone", "availability-zone-id":
		return true
	default:
		return false
	}
}

func containsFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(expected)) {
			return true
		}
	}
	return false
}

func matchesInstanceTypeFromRequirements(instanceType string, data map[string]any, req *api.GetInstanceTypesFromInstanceRequirementsRequest) bool {
	requirements := req.InstanceRequirements
	if requirements == nil {
		return false
	}
	if !matchesTypeSelectors(instanceType, data, req) {
		return false
	}
	if !matchesComputeRequirements(data, requirements) {
		return false
	}
	if !matchesPlatformRequirements(instanceType, data, requirements) {
		return false
	}
	if !matchesNetworkAndStorageRequirements(data, requirements) {
		return false
	}
	if !matchesAcceleratorRequirements(data, requirements) {
		return false
	}
	return true
}

func matchesTypeSelectors(instanceType string, data map[string]any, req *api.GetInstanceTypesFromInstanceRequirementsRequest) bool {
	requirements := req.InstanceRequirements
	if len(requirements.AllowedInstanceTypes) > 0 && !matchesAnyPattern(instanceType, requirements.AllowedInstanceTypes) {
		return false
	}
	if len(requirements.ExcludedInstanceTypes) > 0 && matchesAnyPattern(instanceType, requirements.ExcludedInstanceTypes) {
		return false
	}

	if !intersectsFold(stringSliceAt(data, "ProcessorInfo", "SupportedArchitectures"), req.ArchitectureTypes) {
		return false
	}
	if !intersectsFold(stringSliceAt(data, "SupportedVirtualizationTypes"), req.VirtualizationTypes) {
		return false
	}
	return true
}

func matchesComputeRequirements(data map[string]any, requirements *api.InstanceRequirementsRequest) bool {
	vcpu, ok := int64At(data, "VCpuInfo", "DefaultVCpus")
	if !ok || !matchesIntRange(vcpu, requirements.VCPUCount) {
		return false
	}
	memoryMiB, ok := int64At(data, "MemoryInfo", "SizeInMiB")
	if !ok || !matchesIntRange(memoryMiB, requirements.MemoryMiB) {
		return false
	}
	if requirements.MemoryGiBPerVCPU != nil {
		if vcpu == 0 {
			return false
		}
		memoryGiBPerVCPU := float64(memoryMiB) / 1024.0 / float64(vcpu)
		if !matchesFloatRange(memoryGiBPerVCPU, requirements.MemoryGiBPerVCPU) {
			return false
		}
	}
	if requirements.CPUManufacturers != nil {
		manufacturer, ok := stringAt(data, "ProcessorInfo", "Manufacturer")
		if !ok || !containsFold(requirements.CPUManufacturers, manufacturer) {
			return false
		}
	}
	return true
}

func matchesPlatformRequirements(instanceType string, data map[string]any, requirements *api.InstanceRequirementsRequest) bool {
	if len(requirements.InstanceGenerations) > 0 {
		currentGeneration := boolAt(data, "CurrentGeneration")
		generation := "previous"
		if currentGeneration {
			generation = "current"
		}
		if !containsFold(requirements.InstanceGenerations, generation) {
			return false
		}
	}

	if !matchesInclusionPreference(boolAt(data, "BareMetal"), requirements.BareMetal) {
		return false
	}
	if !matchesInclusionPreference(boolAt(data, "BurstablePerformanceSupported"), requirements.BurstablePerformance) {
		return false
	}
	if !matchesInclusionPreference(boolAt(data, "InstanceStorageSupported"), requirements.LocalStorage) {
		return false
	}
	if requirements.RequireHibernateSupport != nil {
		if boolAt(data, "HibernationSupported") != *requirements.RequireHibernateSupport {
			return false
		}
	}
	if requirements.BaselinePerformanceFactors != nil {
		if !matchesBaselinePerformanceFactors(instanceType, requirements.BaselinePerformanceFactors) {
			return false
		}
	}
	return true
}

func matchesNetworkAndStorageRequirements(data map[string]any, requirements *api.InstanceRequirementsRequest) bool {
	if requirements.BaselineEbsBandwidthMbps != nil {
		baseline, ok := int64At(data, "EbsInfo", "EbsOptimizedInfo", "BaselineBandwidthInMbps")
		if !ok || !matchesIntRange(baseline, requirements.BaselineEbsBandwidthMbps) {
			return false
		}
	}

	if requirements.NetworkInterfaceCount != nil {
		nicCount, ok := int64At(data, "NetworkInfo", "MaximumNetworkInterfaces")
		if !ok || !matchesIntRange(nicCount, requirements.NetworkInterfaceCount) {
			return false
		}
	}
	if requirements.NetworkBandwidthGbps != nil {
		maxBandwidthGbps, ok := maxNetworkBandwidthGbps(data)
		if !ok || !matchesFloatRange(maxBandwidthGbps, requirements.NetworkBandwidthGbps) {
			return false
		}
	}

	if requirements.LocalStorageTypes != nil || requirements.TotalLocalStorageGB != nil {
		localStorageTypes, totalLocalStorage, hasLocalStorage := localStorageSummary(data)
		if requirements.LocalStorageTypes != nil && !intersectsFold(localStorageTypes, requirements.LocalStorageTypes) {
			return false
		}
		if requirements.TotalLocalStorageGB != nil {
			if !hasLocalStorage || !matchesFloatRange(totalLocalStorage, requirements.TotalLocalStorageGB) {
				return false
			}
		}
	}
	return true
}

func matchesAcceleratorRequirements(data map[string]any, requirements *api.InstanceRequirementsRequest) bool {
	accelerator := acceleratorSummary(data)
	if requirements.AcceleratorCount != nil && !matchesIntRange(accelerator.count, requirements.AcceleratorCount) {
		return false
	}
	if requirements.AcceleratorManufacturers != nil && !intersectsFold(accelerator.manufacturers, requirements.AcceleratorManufacturers) {
		return false
	}
	if requirements.AcceleratorNames != nil && !intersectsFold(accelerator.names, requirements.AcceleratorNames) {
		return false
	}
	if requirements.AcceleratorTypes != nil && !intersectsFold(accelerator.types, requirements.AcceleratorTypes) {
		return false
	}
	if requirements.AcceleratorTotalMemoryMiB != nil && !matchesIntRange(accelerator.totalMemoryMiB, requirements.AcceleratorTotalMemoryMiB) {
		return false
	}
	return true
}

func matchesBaselinePerformanceFactors(instanceType string, factors *api.BaselinePerformanceFactorsRequest) bool {
	if factors == nil || factors.CPU == nil || len(factors.CPU.References) == 0 {
		return true
	}
	family := instanceType
	if dot := strings.IndexByte(family, '.'); dot >= 0 {
		family = family[:dot]
	}
	for _, reference := range factors.CPU.References {
		if reference.InstanceFamily == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(*reference.InstanceFamily), family) {
			return true
		}
	}
	return false
}

func matchesAnyPattern(instanceType string, patterns []string) bool {
	candidate := strings.ToLower(strings.TrimSpace(instanceType))
	for _, patternValue := range patterns {
		patternValue = strings.ToLower(strings.TrimSpace(patternValue))
		if patternValue == "" {
			continue
		}
		if strings.ContainsAny(patternValue, "*?[") {
			matched, err := path.Match(patternValue, candidate)
			if err == nil && matched {
				return true
			}
			continue
		}
		if candidate == patternValue {
			return true
		}
	}
	return false
}

func intersectsFold(left []string, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for _, l := range left {
		for _, r := range right {
			if strings.EqualFold(strings.TrimSpace(l), strings.TrimSpace(r)) {
				return true
			}
		}
	}
	return false
}

func matchesInclusionPreference(value bool, preference *string) bool {
	if preference == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(*preference)) {
	case "", "included":
		return true
	case "required":
		return value
	case "excluded":
		return !value
	default:
		return false
	}
}

func matchesIntRange(value int64, rangeValue *api.IntRangeRequest) bool {
	if rangeValue == nil {
		return true
	}
	if rangeValue.Min != nil && value < int64(*rangeValue.Min) {
		return false
	}
	if rangeValue.Max != nil && value > int64(*rangeValue.Max) {
		return false
	}
	return true
}

func matchesFloatRange(value float64, rangeValue *api.FloatRangeRequest) bool {
	if rangeValue == nil {
		return true
	}
	if rangeValue.Min != nil && value < *rangeValue.Min {
		return false
	}
	if rangeValue.Max != nil && value > *rangeValue.Max {
		return false
	}
	return true
}

type acceleratorInfo struct {
	count          int64
	manufacturers  []string
	names          []string
	types          []string
	totalMemoryMiB int64
}

func acceleratorSummary(data map[string]any) acceleratorInfo {
	out := acceleratorInfo{}
	manufacturers := map[string]struct{}{}
	names := map[string]struct{}{}
	types := map[string]struct{}{}

	addAccelerators := func(typeName string, values []any) {
		if len(values) == 0 {
			return
		}
		types[typeName] = struct{}{}
		for _, value := range values {
			item, ok := value.(map[string]any)
			if !ok {
				continue
			}
			count, ok := int64FromAny(item["Count"])
			if !ok || count <= 0 {
				count = 1
			}
			out.count += count
			if manufacturer, ok := stringFromAny(item["Manufacturer"]); ok {
				manufacturers[manufacturer] = struct{}{}
			}
			if name, ok := stringFromAny(item["Name"]); ok {
				names[name] = struct{}{}
			}
		}
	}

	addAccelerators("gpu", anySliceAt(data, "GpuInfo", "Gpus"))
	addAccelerators("inference", anySliceAt(data, "InferenceAcceleratorInfo", "Accelerators"))
	addAccelerators("fpga", anySliceAt(data, "FpgaInfo", "Fpgas"))

	if totalMemory, ok := int64At(data, "GpuInfo", "TotalGpuMemoryInMiB"); ok {
		out.totalMemoryMiB = totalMemory
	}

	out.manufacturers = sortedKeys(manufacturers)
	out.names = sortedKeys(names)
	out.types = sortedKeys(types)
	return out
}

func localStorageSummary(data map[string]any) ([]string, float64, bool) {
	disks := anySliceAt(data, "InstanceStorageInfo", "Disks")
	if len(disks) == 0 {
		return nil, 0, false
	}
	types := map[string]struct{}{}
	total := 0.0
	for _, disk := range disks {
		item, ok := disk.(map[string]any)
		if !ok {
			continue
		}
		if diskType, ok := stringFromAny(item["Type"]); ok {
			types[diskType] = struct{}{}
		}
		sizeGB, ok := float64FromAny(item["SizeInGB"])
		if !ok {
			continue
		}
		count := 1.0
		if rawCount, ok := float64FromAny(item["Count"]); ok && rawCount > 0 {
			count = rawCount
		}
		total += sizeGB * count
	}
	return sortedKeys(types), total, true
}

func maxNetworkBandwidthGbps(data map[string]any) (float64, bool) {
	cards := anySliceAt(data, "NetworkInfo", "NetworkCards")
	maxValue := 0.0
	hasValue := false
	for _, card := range cards {
		item, ok := card.(map[string]any)
		if !ok {
			continue
		}
		bandwidth, ok := float64FromAny(item["PeakBandwidthInGbps"])
		if !ok {
			continue
		}
		if !hasValue || bandwidth > maxValue {
			maxValue = bandwidth
			hasValue = true
		}
	}
	return maxValue, hasValue
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func stringSliceAt(data map[string]any, keys ...string) []string {
	value, ok := pathValue(data, keys...)
	if !ok {
		return nil
	}
	return stringSliceFromAny(value)
}

func anySliceAt(data map[string]any, keys ...string) []any {
	value, ok := pathValue(data, keys...)
	if !ok {
		return nil
	}
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func int64At(data map[string]any, keys ...string) (int64, bool) {
	value, ok := pathValue(data, keys...)
	if !ok {
		return 0, false
	}
	return int64FromAny(value)
}

func stringAt(data map[string]any, keys ...string) (string, bool) {
	value, ok := pathValue(data, keys...)
	if !ok {
		return "", false
	}
	return stringFromAny(value)
}

func boolAt(data map[string]any, keys ...string) bool {
	value, ok := pathValue(data, keys...)
	if !ok {
		return false
	}
	if typed, ok := value.(bool); ok {
		return typed
	}
	return false
}

func pathValue(data map[string]any, keys ...string) (any, bool) {
	if len(keys) == 0 {
		return nil, false
	}
	current := any(data)
	for _, key := range keys {
		typed, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := typed[key]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := slices.Clone(typed)
		slices.Sort(out)
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if str, ok := stringFromAny(item); ok {
				out = append(out, str)
			}
		}
		slices.Sort(out)
		return out
	default:
		return nil
	}
}

func stringFromAny(value any) (string, bool) {
	typed, ok := value.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(typed), true
}

func int64FromAny(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func float64FromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func stringValue(value *string) string {
	if value == nil {
		return "<missing>"
	}
	return *value
}
