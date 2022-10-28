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

package crypto

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is used to get registered crypto provider implementations.
type Registry struct {
	Logger    logger.Logger
	providers map[string]func(logger.Logger) crypto.SubtleCrypto
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry

func init() {
	DefaultRegistry = NewRegistry()
}

// NewRegistry returns a new crypto provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: map[string]func(logger.Logger) crypto.SubtleCrypto{},
	}
}

// RegisterComponent adds a new crypto provider to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) crypto.SubtleCrypto, names ...string) {
	for _, name := range names {
		s.providers[createFullName(name)] = componentFactory
	}
}

// Create instantiates a crypto provider based on `name`.
func (s *Registry) Create(name, version string) (crypto.SubtleCrypto, error) {
	if method, ok := s.getSecretStore(name, version); ok {
		return method(), nil
	}

	return nil, fmt.Errorf("couldn't find crypto provider %s/%s", name, version)
}

func (s *Registry) getSecretStore(name, version string) (func() crypto.SubtleCrypto, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	secretStoreFn, ok := s.providers[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(secretStoreFn), true
	}
	if components.IsInitialVersion(versionLower) {
		secretStoreFn, ok = s.providers[nameLower]
		if ok {
			return s.wrapFn(secretStoreFn), true
		}
	}
	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) crypto.SubtleCrypto) func() crypto.SubtleCrypto {
	return func() crypto.SubtleCrypto {
		return componentFactory(s.Logger)
	}
}

func createFullName(name string) string {
	return strings.ToLower("crypto." + name)
}
