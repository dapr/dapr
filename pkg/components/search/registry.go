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

package search

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/search"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is an interface for a component that returns registered search implementations.
type Registry struct {
	Logger   logger.Logger
	searches map[string]func(logger.Logger) search.Search
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create search registry.
func NewRegistry() *Registry {
	return &Registry{
		Logger:   logger.NewLogger("dapr.search.registry"),
		searches: make(map[string]func(logger.Logger) search.Search),
	}
}

// RegisterComponent adds a new search to the registry.
func (s *Registry) RegisterComponent(componentFactory func(logger.Logger) search.Search, names ...string) {
	for _, name := range names {
		s.searches[createFullName(name)] = componentFactory
	}
}

func (s *Registry) Create(name, version, logName string) (search.Search, error) {
	if method, ok := s.getSearch(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find search %s/%s", name, version)
}

func (s *Registry) getSearch(name, version, logName string) (func() search.Search, bool) {
	name = strings.ToLower(name)
	version = strings.ToLower(version)
	stateStoreFn, ok := s.searches[name+"/"+version]

	if ok {
		return s.wrapFn(stateStoreFn, logName), true
	}
	if components.IsInitialVersion(version) {
		stateStoreFn, ok = s.searches[name]
		if ok {
			return s.wrapFn(stateStoreFn, logName), true
		}
	}

	return nil, false
}

func (s *Registry) wrapFn(componentFactory func(logger.Logger) search.Search, logName string) func() search.Search {
	return func() search.Search {
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
	return strings.ToLower("search." + name)
}
