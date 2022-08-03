package metadata

import (
	"sync"
)

// MetadataStore is an interface to perform operations on metadata store.
type MetadataStore interface {
	MetadataGet() (map[string]string, error)
	MetadataSet(key, value string) error
}

// DefaultMetadataStore is a default implementation of MetadataStore.
type DefaultMetadataStore struct {
	metadata sync.Map
}

// NewDefaultMetadataStore build a default metadata store.
func NewDefaultMetadataStore() DefaultMetadataStore {
	return DefaultMetadataStore{}
}

// MetadataGet get metadata of sidecar.
func (m *DefaultMetadataStore) MetadataGet() (map[string]string, error) {
	temp := make(map[string]string)

	// Copy synchronously so it can be serialized to JSON.
	m.metadata.Range(func(key, value interface{}) bool {
		temp[key.(string)] = value.(string)

		return true
	})

	return temp, nil
}

// MetadataSet performs a metadata save operation.
func (m *DefaultMetadataStore) MetadataSet(key, value string) error {
	m.metadata.Store(key, value)
	return nil
}
