// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicediscovery

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/servicediscovery"
)

type Registry interface {
	CreateResolver(name string) (servicediscovery.Resolver, error)
}

type serviceDiscoveryRegistry struct {
	resolvers map[string]func() servicediscovery.Resolver
}

var instance *serviceDiscoveryRegistry
var once sync.Once

func NewRegistry() Registry {
	once.Do(func() {
		instance = &serviceDiscoveryRegistry{
			resolvers: map[string]func() servicediscovery.Resolver{},
		}
	})
	return instance
}

func RegisterServiceDiscovery(name string, factoryMethod func() servicediscovery.Resolver) {
	instance.resolvers[formatName(name)] = factoryMethod
}

func (s *serviceDiscoveryRegistry) CreateResolver(name string) (servicediscovery.Resolver, error) {
	if method, ok := s.resolvers[formatName(name)]; ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find service resolver %s", name)
}

func formatName(s string) string {
	return fmt.Sprintf("servicediscovery.%s", s)
}
