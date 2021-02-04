// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"strings"

	"github.com/dapr/components-contrib/state"
	"github.com/pkg/errors"
)

type State struct {
	Name          string
	FactoryMethod func() state.Store
}

func New(name string, factoryMethod func() state.Store) State {
	return State{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// Registry is an interface for a component that returns registered state store implementations
type Registry interface {
	Register(components ...State)
	Create(name, version string) (state.Store, error)
}

type stateStoreRegistry struct {
	stateStores map[string]func() state.Store
}

// NewRegistry is used to create state store registry.
func NewRegistry() Registry {
	return &stateStoreRegistry{
		stateStores: map[string]func() state.Store{},
	}
}

// // Register registers a new factory method that creates an instance of a StateStore.
// // The key is the name of the state store, eg. redis.
func (s *stateStoreRegistry) Register(components ...State) {
	for _, component := range components {
		s.stateStores[createFullName(component.Name)] = component.FactoryMethod
	}
}

func (s *stateStoreRegistry) Create(name, version string) (state.Store, error) {
	if method, ok := s.getSecretStore(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find state store %s/%s", name, version)
}

func (s *stateStoreRegistry) getSecretStore(name, version string) (func() state.Store, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	stateStoreFn, ok := s.stateStores[nameLower+"/"+versionLower]
	if ok {
		return stateStoreFn, true
	}
	switch versionLower {
	case "", "v0", "v1":
		stateStoreFn, ok = s.stateStores[nameLower]
	}
	return stateStoreFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("state." + name)
}
