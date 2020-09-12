// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores

import (
	"fmt"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/pkg/errors"
)

type (
	// SecretStore is a secret store component definition.
	SecretStore struct {
		Name          string
		FactoryMethod func() secretstores.SecretStore
	}

	// Registry is used to get registered secret store implementations
	Registry interface {
		Register(components ...SecretStore)
		Create(name string) (secretstores.SecretStore, error)
	}

	secretStoreRegistry struct {
		secretStores map[string]func() secretstores.SecretStore
	}
)

// New creates a SecretStore.
func New(name string, factoryMethod func() secretstores.SecretStore) SecretStore {
	return SecretStore{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry returns a new secret store registry.
func NewRegistry() Registry {
	return &secretStoreRegistry{
		secretStores: map[string]func() secretstores.SecretStore{},
	}
}

// Register adds one or many new secret stores to the registry.
func (s *secretStoreRegistry) Register(components ...SecretStore) {
	for _, component := range components {
		s.secretStores[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a secret store based on `name`.
func (s *secretStoreRegistry) Create(name string) (secretstores.SecretStore, error) {
	if method, ok := s.secretStores[name]; ok {
		return method(), nil
	}

	return nil, errors.Errorf("couldn't find secret store %s", name)
}

func createFullName(name string) string {
	return fmt.Sprintf("secretstores.%s", name)
}
