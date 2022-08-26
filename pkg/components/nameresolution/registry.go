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

package nameresolution

import (
	"strings"

	"github.com/pkg/errors"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/components"
)

type (
	FactoryMethod func(logger.Logger) nr.Resolver

	// Registry handles registering and creating name resolution components.
	Registry struct {
		Logger    logger.Logger
		resolvers map[string]FactoryMethod
	}
)

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry

func init() {
	DefaultRegistry = NewRegistry()
}

// NewRegistry creates a name resolution registry.
func NewRegistry() *Registry {
	return &Registry{
		resolvers: map[string]FactoryMethod{},
	}
}

// RegisterComponent adds a name resolver to the registry.
func (s *Registry) RegisterComponent(componentFactory FactoryMethod, names ...string) {
	for _, name := range names {
		s.resolvers[createFullName(name)] = componentFactory
	}
}

// Create instantiates a name resolution resolver based on `name`.
func (s *Registry) Create(name, version string) (nr.Resolver, error) {
	if method, ok := s.getResolver(createFullName(name), version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find name resolver %s/%s", name, version)
}

func (s *Registry) getResolver(name, version string) (func() nr.Resolver, bool) {
	if s.resolvers == nil {
		return nil, false
	}
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	resolverFn, ok := s.resolvers[nameLower+"/"+versionLower]
	if ok {
		return s.wrapFn(resolverFn), true
	}
	if components.IsInitialVersion(versionLower) {
		resolverFn, ok = s.resolvers[nameLower]
		if ok {
			return s.wrapFn(resolverFn), true
		}
	}
	return nil, false
}

func (s *Registry) wrapFn(componentFactory FactoryMethod) func() nr.Resolver {
	return func() nr.Resolver {
		return componentFactory(s.Logger)
	}
}

func createFullName(name string) string {
	return strings.ToLower("nameresolution." + name)
}
