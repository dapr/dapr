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
	// versionsSet holds a set of component types version information for
	// component types that have multiple versions.
	versionsSet map[string]components.Versioning
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create state store registry.
func NewRegistry() *Registry {
	return &Registry{
		Logger:      logger.NewLogger("dapr.state.registry"),
		stateStores: make(map[string]func(logger.Logger) state.Store),
		versionsSet: make(map[string]components.Versioning),
	}
}

// RegisterComponent adds a new state store to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) state.Store, names ...string) {
	for _, name := range names {
		s.stateStores[createFullName(name)] = componentFactory
	}
}

// RegisterComponent adds a new state store to the registry.
func (s *Registry) RegisterComponentWithVersions(name string, versions components.Versioning) {
	if len(versions.Default) == 0 {
		// Panicking here is appropriate because this is a programming error, and
		// will happen at init time when registering components.
		// An error here is impossible to resolve at runtime, and code change
		// always needs to take place.
		panic(fmt.Sprintf("default version not set for %s", name))
	}

	s.stateStores[createFullVersionedName(name, versions.Preferred.Version)] = toConstructor(versions.Preferred)
	for _, version := range append(versions.Others, versions.Deprecated...) {
		s.stateStores[createFullVersionedName(name, version.Version)] = toConstructor(version)
	}

	s.versionsSet[createFullName(name)] = versions
}

func toConstructor(cv components.VersionConstructor) func(logger.Logger) state.Store {
	fn, ok := cv.Constructor.(func(logger.Logger) state.Store)
	if !ok {
		// Panicking here is appropriate because this is a programming error, and
		// will happen at init time when registering components.
		// An error here is impossible to resolve at runtime, and code change
		// always needs to take place.
		panic(fmt.Sprintf("constructor for %s is not a state store", cv.Version))
	}
	return fn
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

	if ver, ok := s.versionsSet[name]; ok {
		// Default the version when an empty version string is passed, and component
		// has multiple versions.
		if len(version) == 0 {
			version = ver.Default
		}

		components.CheckDeprecated(s.Logger, name, version, s.versionsSet[name])
	}

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

func createFullVersionedName(name, version string) string {
	return strings.ToLower("state." + name + "/" + version)
}
