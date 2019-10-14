// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/secretstores"
)

// Registry is used to get registered secret store implementations
type Registry interface {
	CreateSecretStore(name string) (secretstores.SecretStore, error)
}

type secretStoreRegistry struct {
	secretStores map[string]func() secretstores.SecretStore
}

var instance *secretStoreRegistry
var once sync.Once

// NewRegistry returns a new secret store registry
func NewRegistry() Registry {
	once.Do(func() {
		instance = &secretStoreRegistry{
			secretStores: map[string]func() secretstores.SecretStore{},
		}
	})
	return instance
}

// RegisterSecretStore registers a new secret store
func RegisterSecretStore(name string, factoryMethod func() secretstores.SecretStore) {
	instance.secretStores[createFullName(name)] = factoryMethod
}

func createFullName(name string) string {
	return fmt.Sprintf("secretstores.%s", name)
}

func (s *secretStoreRegistry) CreateSecretStore(name string) (secretstores.SecretStore, error) {
	if method, ok := s.secretStores[name]; ok {
		return method(), nil
	}

	return nil, fmt.Errorf("couldn't find secret store %s", name)
}
