package secretstores

import (
	"fmt"
	"sync"
)

// SecretStoreRegistry is used to get registered secret store implementations
type SecretStoreRegistry interface {
	CreateSecretStore(name string) (SecretStore, error)
}

type secretStoreRegistry struct {
	secretStores map[string]SecretStore
}

var instance *secretStoreRegistry
var once sync.Once

// NewSecretStoreRegistry returns a new secret store registry
func NewSecretStoreRegistry() SecretStoreRegistry {
	once.Do(func() {
		instance = &secretStoreRegistry{
			secretStores: map[string]SecretStore{},
		}
	})
	return instance
}

// RegisterSecretStore registers a new secret store
func RegisterSecretStore(name string, secretStore SecretStore) {
	instance.secretStores[createFullName(name)] = secretStore
}

func createFullName(name string) string {
	return fmt.Sprintf("secretstores.%s", name)
}

func (s *secretStoreRegistry) CreateSecretStore(name string) (SecretStore, error) {
	if val, ok := s.secretStores[name]; ok {
		return val, nil
	}

	return nil, fmt.Errorf("couldn't find secret store %s", name)
}
