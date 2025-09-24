/*
Copyright 2023 The Dapr Authors
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
var DefaultRegistry *Registry = NewRegistry()

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
func (s *Registry) Create(name, version, logName string) (crypto.SubtleCrypto, error) {
	if method, ok := s.getCryptoProvider(name, version, logName); ok {
		return method(), nil
	}

	return nil, fmt.Errorf("couldn't find crypto provider %s/%s", name, version)
}

func (s *Registry) getCryptoProvider(name, version, logName string) (func() crypto.SubtleCrypto, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	cryptoProviderFn, ok := s.providers[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(cryptoProviderFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		cryptoProviderFn, ok = s.providers[nameLower]
		if ok {
			return s.wrapFn(cryptoProviderFn, logName), true
		}
	}
	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) crypto.SubtleCrypto, logName string) func() crypto.SubtleCrypto {
	return func() crypto.SubtleCrypto {
		l := s.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(l)
	}
}

func createFullName(name string) string {
	return strings.ToLower("crypto." + name)
}
