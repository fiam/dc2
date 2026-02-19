package instancetype

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
)

//go:embed data/instance_types.json
var embeddedCatalogJSON []byte

type Offering struct {
	InstanceType string `json:"InstanceType"`
	Location     string `json:"Location"`
	LocationType string `json:"LocationType"`
}

type Catalog struct {
	InstanceTypes map[string]map[string]any `json:"instance_types"`
	Offerings     []Offering                `json:"offerings"`
}

var (
	defaultCatalogOnce sync.Once
	defaultCatalog     *Catalog
	defaultCatalogErr  error
)

func LoadDefault() (*Catalog, error) {
	defaultCatalogOnce.Do(func() {
		defaultCatalog, defaultCatalogErr = load(embeddedCatalogJSON)
	})
	if defaultCatalogErr != nil {
		return nil, defaultCatalogErr
	}
	return defaultCatalog.Clone(), nil
}

func load(raw []byte) (*Catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var parsed Catalog
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decoding instance type catalog: %w", err)
	}
	if parsed.InstanceTypes == nil {
		parsed.InstanceTypes = map[string]map[string]any{}
	}
	if parsed.Offerings == nil {
		parsed.Offerings = []Offering{}
	}

	normalized := &Catalog{
		InstanceTypes: make(map[string]map[string]any, len(parsed.InstanceTypes)),
		Offerings:     make([]Offering, 0, len(parsed.Offerings)),
	}
	for instanceType, info := range parsed.InstanceTypes {
		infoCopy, ok := normalizeValue(info).(map[string]any)
		if !ok {
			return nil, fmt.Errorf("instance type %q payload must be an object", instanceType)
		}
		normalized.InstanceTypes[instanceType] = infoCopy
	}

	seenOfferings := map[string]struct{}{}
	for _, offering := range parsed.Offerings {
		instanceType := strings.TrimSpace(offering.InstanceType)
		location := strings.TrimSpace(offering.Location)
		locationType := strings.TrimSpace(offering.LocationType)
		if instanceType == "" || location == "" || locationType == "" {
			continue
		}
		key := strings.ToLower(instanceType + "|" + locationType + "|" + location)
		if _, exists := seenOfferings[key]; exists {
			continue
		}
		seenOfferings[key] = struct{}{}
		normalized.Offerings = append(normalized.Offerings, Offering{
			InstanceType: instanceType,
			LocationType: locationType,
			Location:     location,
		})
	}
	sort.Slice(normalized.Offerings, func(i, j int) bool {
		if normalized.Offerings[i].LocationType != normalized.Offerings[j].LocationType {
			return normalized.Offerings[i].LocationType < normalized.Offerings[j].LocationType
		}
		if normalized.Offerings[i].Location != normalized.Offerings[j].Location {
			return normalized.Offerings[i].Location < normalized.Offerings[j].Location
		}
		return normalized.Offerings[i].InstanceType < normalized.Offerings[j].InstanceType
	})

	return normalized, nil
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = normalizeValue(value)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, value := range typed {
			out = append(out, normalizeValue(value))
		}
		return out
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		floatValue, err := typed.Float64()
		if err != nil {
			return typed.String()
		}
		return floatValue
	default:
		return value
	}
}

func (c *Catalog) Clone() *Catalog {
	out := &Catalog{
		InstanceTypes: maps.Clone(c.InstanceTypes),
		Offerings:     slices.Clone(c.Offerings),
	}
	for instanceType, info := range out.InstanceTypes {
		typed, ok := cloneValue(info).(map[string]any)
		if !ok {
			out.InstanceTypes[instanceType] = map[string]any{}
			continue
		}
		out.InstanceTypes[instanceType] = typed
	}
	return out
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := maps.Clone(typed)
		for key, raw := range cloned {
			cloned[key] = cloneValue(raw)
		}
		return cloned
	case []any:
		cloned := slices.Clone(typed)
		for i := range cloned {
			cloned[i] = cloneValue(cloned[i])
		}
		return cloned
	default:
		return typed
	}
}
