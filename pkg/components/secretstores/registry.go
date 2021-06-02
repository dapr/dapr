// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores

import (
	"strings"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/secretstores"

	"github.com/dapr/dapr/pkg/components"
)

type (
	// SecretStore is a secret store component definition.
	SecretStore struct {
		Name          string
		FactoryMethod func() secretstores.SecretStore
	}

	// Registry is used to get registered secret store implementations.
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
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	secretStoreFn, ok := s.secretStores[nameLower+"/"+versionLower]
	if ok {
		return secretStoreFn, true
	}
	if components.IsInitialVersion(versionLower) {
		secretStoreFn, ok = s.secretStores[nameLower]
	}
	return secretStoreFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("secretstores." + name)
}
