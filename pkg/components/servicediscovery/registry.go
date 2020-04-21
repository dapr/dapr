// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicediscovery

import (
	"fmt"

	"github.com/dapr/components-contrib/servicediscovery"
)

type (
	// ServiceDiscovery is a service discovery component definition.
	ServiceDiscovery struct {
		Name          string
		FactoryMethod func() servicediscovery.Resolver
	}

	// Registry handles registering and creating service discovery components.
	Registry interface {
		Register(components ...ServiceDiscovery)
		Create(name string) (servicediscovery.Resolver, error)
	}

	serviceDiscoveryRegistry struct {
		resolvers map[string]func() servicediscovery.Resolver
	}
)

// New creates a ServiceDiscovery.
func New(name string, factoryMethod func() servicediscovery.Resolver) ServiceDiscovery {
	return ServiceDiscovery{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry creates a servce discovery registry.
func NewRegistry() Registry {
	return &serviceDiscoveryRegistry{
		resolvers: map[string]func() servicediscovery.Resolver{},
	}
}

// Register adds one or many service discovery components to the registry.
func (s *serviceDiscoveryRegistry) Register(components ...ServiceDiscovery) {
	for _, component := range components {
		s.resolvers[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates a service discovery resolver based on `name`.
func (s *serviceDiscoveryRegistry) Create(name string) (servicediscovery.Resolver, error) {
	if method, ok := s.resolvers[createFullName(name)]; ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find service resolver %s", name)
}

func createFullName(name string) string {
	return fmt.Sprintf("servicediscovery.%s", name)
}
