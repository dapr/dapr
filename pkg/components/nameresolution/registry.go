// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package nameresolution

import (
	"fmt"

	nr "github.com/dapr/components-contrib/nameresolution"
	"github.com/pkg/errors"
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
		Create(name string) (nr.Resolver, error)
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
func (s *nameResolutionRegistry) Create(name string) (nr.Resolver, error) {
	if method, ok := s.resolvers[createFullName(name)]; ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find name resolver %s", name)
}

func createFullName(name string) string {
	return fmt.Sprintf("nameresolution.%s", name)
}
