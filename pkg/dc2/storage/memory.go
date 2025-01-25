package storage

import (
	"slices"

	"github.com/fiam/dc2/pkg/dc2/types"
)

type resourceStorage struct {
	Type  types.ResourceType
	Attrs map[string]string
}

type memoryStorage struct {
	resources map[string]*resourceStorage
}

func NewMemoryStorage() Storage {
	return &memoryStorage{
		resources: make(map[string]*resourceStorage),
	}
}

func (s *memoryStorage) RegisterResource(r Resource) error {
	if _, ok := s.resources[r.ID]; ok {
		return ErrDuplicatedResource{ID: r.ID}
	}
	s.resources[r.ID] = &resourceStorage{
		Type: r.Type,
	}
	return nil
}

func (s *memoryStorage) RemoveResource(id string) error {
	if _, ok := s.resources[id]; !ok {
		return ErrResourceNotFound{ID: id}
	}
	delete(s.resources, id)
	return nil
}

func (s *memoryStorage) RegisteredResources(rt types.ResourceType) ([]Resource, error) {
	resources := make([]Resource, 0, len(s.resources))
	for id, r := range s.resources {
		if r.Type == rt {
			resources = append(resources, Resource{Type: rt, ID: id})
		}
	}
	slices.SortFunc(resources, func(a Resource, b Resource) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return resources, nil
}

func (s *memoryStorage) SetResourceAttributes(id string, attrs []Attribute) error {
	resource, ok := s.resources[id]
	if !ok {
		return ErrResourceNotFound{ID: id}
	}
	if resource.Attrs == nil {
		resource.Attrs = make(map[string]string)
	}
	for _, attr := range attrs {
		resource.Attrs[attr.Key] = attr.Value
	}
	return nil
}

func (s *memoryStorage) RemoveResourceAttributes(id string, attrs []Attribute) error {
	resource, ok := s.resources[id]
	if !ok {
		return ErrResourceNotFound{ID: id}
	}
	for _, attr := range attrs {
		if attr.Value == "" || resource.Attrs[attr.Key] == attr.Value {
			delete(resource.Attrs, attr.Key)
		}
	}
	return nil
}

func (s *memoryStorage) ResourceAttributes(id string) (Attributes, error) {
	r, ok := s.resources[id]
	if !ok {
		return nil, ErrResourceNotFound{ID: id}
	}
	attrsList := make([]Attribute, 0, len(r.Attrs))
	for k, v := range r.Attrs {
		attrsList = append(attrsList, Attribute{Key: k, Value: v})
	}
	slices.SortFunc(attrsList, func(a Attribute, b Attribute) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})
	return attrsList, nil
}
