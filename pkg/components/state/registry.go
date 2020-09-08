// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"fmt"

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
	CreateStateStore(name string) (state.Store, error)
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

func (s *stateStoreRegistry) CreateStateStore(name string) (state.Store, error) {
	if method, ok := s.stateStores[name]; ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find state store %s", name)
}

func createFullName(name string) string {
	return fmt.Sprintf("state.%s", name)
}
