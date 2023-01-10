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
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Name of the built-in Kubernetes secret store component.
const BuiltinKubernetesSecretStore = "kubernetes"

// Registry is used to get registered secret store implementations.
type Registry struct {
	Logger       logger.Logger
	secretStores map[string]func(logger.Logger) secretstores.SecretStore
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry

func init() {
	DefaultRegistry = NewRegistry()
}

// NewRegistry returns a new secret store registry.
func NewRegistry() *Registry {
	return &Registry{
		secretStores: map[string]func(logger.Logger) secretstores.SecretStore{},
	}
}

// RegisterComponent adds a new secret store to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) secretstores.SecretStore, names ...string) {
	for _, name := range names {
		s.secretStores[createFullName(name)] = componentFactory
	}
}

// Create instantiates a secret store based on `name`.
func (s *Registry) Create(name, version string) (secretstores.SecretStore, error) {
	if method, ok := s.getSecretStore(name, version); ok {
		return method(), nil
	}

	return nil, fmt.Errorf("couldn't find secret store %s/%s", name, version)
}

func (s *Registry) getSecretStore(name, version string) (func() secretstores.SecretStore, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	secretStoreFn, ok := s.secretStores[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(secretStoreFn), true
	}
	if components.IsInitialVersion(versionLower) {
		secretStoreFn, ok = s.secretStores[nameLower]
		if ok {
			return s.wrapFn(secretStoreFn), true
		}
	}
	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) secretstores.SecretStore) func() secretstores.SecretStore {
	return func() secretstores.SecretStore {
		return componentFactory(s.Logger)
	}
}

func createFullName(name string) string {
	return strings.ToLower("secretstores." + name)
}
