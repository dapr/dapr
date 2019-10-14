// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/state"
)

// Registry is an interface for a component that returns registered state store implementations
type Registry interface {
	CreateStateStore(name string) (state.StateStore, error)
}

type stateStoreRegistry struct {
	stateStores map[string]func() state.StateStore
}

var instance *stateStoreRegistry
var once sync.Once

// NewStateStoreRegistry is used to create state store registry
func NewStateStoreRegistry() Registry {
	once.Do(func() {
		instance = &stateStoreRegistry{
			stateStores: map[string]func() state.StateStore{},
		}
	})
	return instance
}

// RegisterStateStore registers a new factory method that creates an instance of a StateStore.
// The key is the name of the state store, eg. redis
func RegisterStateStore(name string, factoryMethod func() state.StateStore) {
	instance.stateStores[fmt.Sprintf("state.%s", name)] = factoryMethod
}

func (s *stateStoreRegistry) CreateStateStore(name string) (state.StateStore, error) {
	for key, method := range s.stateStores {
		if key == name {
			return method(), nil
		}
	}
	return nil, fmt.Errorf("couldn't find state store %s", name)
}
