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
	// verSet holds a set of component types version information for components
	// which have multiple versions.
	verSet map[string]components.Versioning
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create state store registry.
func NewRegistry() *Registry {
	return &Registry{
		stateStores: map[string]func(logger.Logger) state.Store{},
		verSet: map[string]components.Versioning{
			"state.etcd": components.Versioning{
				Default:    "v1",
				Preferred:  "v2",
				Deprecated: []string{"v1"},
			},
		},
	}
}

// RegisterComponent adds a new state store to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) state.Store, names ...string) {
	for _, name := range names {
		s.stateStores[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (state.Store, error) {
	if method, ok := s.getStateStore(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find state store %s/%s", name, version)
}

func (s *Registry) getStateStore(name, version, logName string) (func() state.Store, bool) {
	name = strings.ToLower(name)
	version = strings.ToLower(version)

	// Default the version if empty string and component has multiple versions.
	if ver, ok := s.verSet[name]; ok {
		if len(version) == 0 {
			version = ver.Default
		}
	}

	components.CheckDeprecated(s.Logger, name, version, s.verSet)

	stateStoreFn, ok := s.stateStores[name+"/"+version]
	if ok {
		return s.wrapFn(stateStoreFn, logName), true
	}
	if components.IsInitialVersion(version) {
		stateStoreFn, ok = s.stateStores[name]
		if ok {
			return s.wrapFn(stateStoreFn, logName), true
		}
	}

	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) state.Store, logName string) func() state.Store {
	return func() state.Store {
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
	return strings.ToLower("state." + name)
}
