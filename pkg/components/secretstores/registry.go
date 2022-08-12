/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package secretstores

import (
	"strings"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/components"
)

// Name of the built-in Kubernetes secret store component.
const BuiltinKubernetesSecretStore = "kubernetes"

type (
	// SecretStore is a secret store component definition.
	SecretStore struct {
		Names         []string
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
func New(name string, factoryMethod func() secretstores.SecretStore, aliases ...string) SecretStore {
	names := []string{name}
	if len(aliases) > 0 {
		names = append(names, aliases...)
	}
	return SecretStore{
		Names:         names,
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
		for _, name := range component.Names {
			s.secretStores[createFullName(name)] = component.FactoryMethod
		}
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
