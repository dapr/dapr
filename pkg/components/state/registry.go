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

package state

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered state store implementations.
type Registry struct {
	Logger      logger.Logger
	stateStores map[string]func(logger.Logger) state.Store
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create state store registry.
func NewRegistry() *Registry {
	return &Registry{
		stateStores: map[string]func(logger.Logger) state.Store{},
	}
}

// RegisterComponent adds a new state store to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) state.Store, names ...string) {
	for _, name := range names {
		s.stateStores[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version string) (state.Store, error) {
	if method, ok := s.getStateStore(name, version); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find state store %s/%s", name, version)
}

func (s *Registry) getStateStore(name, version string) (func() state.Store, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	stateStoreFn, ok := s.stateStores[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(stateStoreFn), true
	}
	if components.IsInitialVersion(versionLower) {
		stateStoreFn, ok = s.stateStores[nameLower]
		if ok {
			return s.wrapFn(stateStoreFn), true
		}
	}

	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) state.Store) func() state.Store {
	return func() state.Store {
		return componentFactory(s.Logger)
	}
}

func createFullName(name string) string {
	return strings.ToLower("state." + name)
}
