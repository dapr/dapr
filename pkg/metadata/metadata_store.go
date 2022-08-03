package metadata

import (
	"sync"
)

// Store is an interface to perform operations on metadata store.
type Store interface {
	MetadataGet() (map[string]string, error)
	MetadataSet(key, value string) error
}

// DefaultStore is a default implementation of Store.
type DefaultStore struct {
	metadata sync.Map
}

// NewDefaultStore build a default metadata store.
func NewDefaultStore() DefaultStore {
	return DefaultStore{}
}

// MetadataGet get metadata of sidecar.
func (m *DefaultStore) MetadataGet() (map[string]string, error) {
	temp := make(map[string]string)

	// Copy synchronously so it can be serialized to JSON.
	m.metadata.Range(func(key, value interface{}) bool {
		temp[key.(string)] = value.(string)

		return true
	})

	return temp, nil
}

// MetadataSet performs a metadata save operation.
func (m *DefaultStore) MetadataSet(key, value string) error {
	m.metadata.Store(key, value)
	return nil
}
