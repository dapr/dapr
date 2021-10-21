// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package configuration

import (
	"strings"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/dapr/pkg/components"
)

type Configuration struct {
	Name          string
	FactoryMethod func() configuration.Store
}

func New(name string, factoryMethod func() configuration.Store) Configuration {
	return Configuration{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// Registry is an interface for a component that returns registered configuration store implementations.
type Registry interface {
	Register(components ...Configuration)
	Create(name, version string) (configuration.Store, error)
}

type configurationStoreRegistry struct {
	configurationStores map[string]func() configuration.Store
}

// NewRegistry is used to create configuration store registry.
func NewRegistry() Registry {
	return &configurationStoreRegistry{
		configurationStores: map[string]func() configuration.Store{},
	}
}

// Register registers a new factory method that creates an instance of a ConfigurationStore.
// The key is the name of the state store, eg. redis.
func (s *configurationStoreRegistry) Register(components ...Configuration) {
	for _, component := range components {
		s.configurationStores[createFullName(component.Name)] = component.FactoryMethod
	}
}

func (s *configurationStoreRegistry) Create(name, version string) (configuration.Store, error) {
	if method, ok := s.getConfigurationStore(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find configuration store %s/%s", name, version)
}

func (s *configurationStoreRegistry) getConfigurationStore(name, version string) (func() configuration.Store, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	configurationStoreFn, ok := s.configurationStores[nameLower+"/"+versionLower]
	if ok {
		return configurationStoreFn, true
	}
	if components.IsInitialVersion(versionLower) {
		configurationStoreFn, ok = s.configurationStores[nameLower]
	}
	return configurationStoreFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("configuration." + name)
}
