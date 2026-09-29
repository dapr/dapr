/*
Copyright 2026 The Dapr Authors
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

package vector

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/vector"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered vector implementations.
type Registry struct {
	Logger  logger.Logger
	vectors map[string]func(logger.Logger) vector.Vector
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create vector registry.
func NewRegistry() *Registry {
	return &Registry{
		Logger:  logger.NewLogger("dapr.vector.registry"),
		vectors: make(map[string]func(logger.Logger) vector.Vector),
	}
}

// RegisterComponent adds a new vector to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) vector.Vector, names ...string) {
	for _, name := range names {
		s.vectors[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (vector.Vector, error) {
	if method, ok := s.getVector(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find vector %s/%s", name, version)
}

func (s *Registry) getVector(name, version, logName string) (func() vector.Vector, bool) {
	name = strings.ToLower(name)
	version = strings.ToLower(version)
	stateStoreFn, ok := s.vectors[name+"/"+version]

	if ok {
		return s.wrapFn(stateStoreFn, logName), true
	}
	if components.IsInitialVersion(version) {
		stateStoreFn, ok = s.vectors[name]
		if ok {
			return s.wrapFn(stateStoreFn, logName), true
		}
	}

	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) vector.Vector, logName string) func() vector.Vector {
	return func() vector.Vector {
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
	return strings.ToLower("vector." + name)
}
