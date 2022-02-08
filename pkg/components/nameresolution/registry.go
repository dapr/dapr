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

	"github.com/dapr/dapr/pkg/components"
)

type (
	// NameResolution is a name resolution component definition.
	NameResolution struct {
		Name          string
		FactoryMethod func() nr.Resolver
	}

	// Registry handles registering and creating name resolution components.
	Registry interface {
		Register(components ...NameResolution)
		Create(name, version string) (nr.Resolver, error)
	}

	nameResolutionRegistry struct {
		resolvers map[string]func() nr.Resolver
	}
)

// New creates a NameResolution.
func New(name string, factoryMethod func() nr.Resolver) NameResolution {
	return NameResolution{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry creates a name resolution registry.
func NewRegistry() Registry {
	return &nameResolutionRegistry{
		resolvers: map[string]func() nr.Resolver{},
	}
}

// Register adds one or many name resolution components to the registry.
func (s *nameResolutionRegistry) Register(components ...NameResolution) {
	for _, component := range components {
		s.resolvers[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a name resolution resolver based on `name`.
func (s *nameResolutionRegistry) Create(name, version string) (nr.Resolver, error) {
	if method, ok := s.getResolver(createFullName(name), version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find name resolver %s/%s", name, version)
}

func (s *nameResolutionRegistry) getResolver(name, version string) (func() nr.Resolver, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	resolverFn, ok := s.resolvers[nameLower+"/"+versionLower]
	if ok {
		return resolverFn, true
	}
	if components.IsInitialVersion(versionLower) {
		resolverFn, ok = s.resolvers[nameLower]
	}
	return resolverFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("nameresolution." + name)
}
