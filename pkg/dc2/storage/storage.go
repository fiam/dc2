package storage

import (
	"strings"

	"github.com/fiam/dc2/pkg/dc2/types"
)

const (
	tagPrefix = "tag:"
)

type Resource struct {
	Type types.ResourceType
	ID   string
}

type Attribute struct {
	Key   string
	Value string
}

// IsTag returns true if the attribute is a tag
func (a Attribute) IsTag() bool {
	return strings.HasPrefix(a.Key, tagPrefix)
}

// TagKey returns the tag key of the attribute if the attribute is a tag.
// If the attribute is not a tag, it returns an empty string.
func (a Attribute) TagKey() string {
	if strings.HasPrefix(a.Key, tagPrefix) {
		return a.Key[len(tagPrefix):]
	}
	return ""
}

type Attributes []Attribute

func (attrs Attributes) Key(key string) (string, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value, true
		}
	}
	return "", false
}

func TagAttributeName(key string) string {
	return tagPrefix + key
}

type Storage interface {
	RegisterResource(r Resource) error
	RemoveResource(id string) error
	RegisteredResources(rt types.ResourceType) ([]Resource, error)
	// SetResourceAttributes sets the attributes of the resource with the given id,
	// replacing any existing attributes with the same key.
	SetResourceAttributes(id string, attrs []Attribute) error
	// RemoveResourceAttributes removes the attributes with the given keys from the resource with the given id.
	// If the attribute does not have a value, it removes all attributes with the given key. If the value is specified
	// it only removes the attribute with the given key and value. If the attribute does not exist, it does nothing.
	RemoveResourceAttributes(id string, attrs []Attribute) error
	ResourceAttributes(id string) (Attributes, error)
}
