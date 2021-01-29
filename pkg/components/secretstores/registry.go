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
		Create(name, version string) (secretstores.SecretStore, error)
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
func (s *secretStoreRegistry) Create(name, version string) (secretstores.SecretStore, error) {
	if method, ok := s.getSecretStore(name, version); ok {
		return method(), nil
	}

	return nil, errors.Errorf("couldn't find secret store %s/%s", name, version)
}

func (s *secretStoreRegistry) getSecretStore(name, version string) (func() secretstores.SecretStore, bool) {
	secretStoreFn, ok := s.secretStores[name+"/"+version]
	if ok {
		return secretStoreFn, true
	}
	if version == "" || version == "v0" || version == "v1" {
		secretStoreFn, ok = s.secretStores[name]
	}
	return secretStoreFn, ok
}

func createFullName(name string) string {
	return fmt.Sprintf("secretstores.%s", name)
}
