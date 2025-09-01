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

package configuration

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered configuration store implementations.
type Registry struct {
	Logger              logger.Logger
	configurationStores map[string]func(logger.Logger) configuration.Store
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create configuration store registry.
func NewRegistry() *Registry {
	return &Registry{
		configurationStores: map[string]func(logger.Logger) configuration.Store{},
	}
}

func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) configuration.Store, names ...string) {
	for _, name := range names {
		s.configurationStores[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (configuration.Store, error) {
	if method, ok := s.getConfigurationStore(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find configuration store %s/%s", name, version)
}

func (s *Registry) getConfigurationStore(name, version, logName string) (func() configuration.Store, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	configurationStoreFn, ok := s.configurationStores[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(configurationStoreFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		configurationStoreFn, ok = s.configurationStores[nameLower]
		if ok {
			return s.wrapFn(configurationStoreFn, logName), true
		}
	}
	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) configuration.Store, logName string) func() configuration.Store {
	return func() configuration.Store {
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
	return strings.ToLower("configuration." + name)
}
