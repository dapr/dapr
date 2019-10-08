package state

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/state"
)

// StateStoreRegistry is an abstraction to create state store
type StateStoreRegistry interface {
	CreateStateStore(name string) (state.StateStore, error)
}

type stateStoreRegistry struct {
	stateStores map[string]state.StateStore
}

var instance *stateStoreRegistry
var once sync.Once

// NewStateStoreRegistry is used to create state store registry
func NewStateStoreRegistry() StateStoreRegistry {
	once.Do(func() {
		instance = &stateStoreRegistry{
			stateStores: map[string]state.StateStore{},
		}
	})
	return instance
}

func RegisterStateStore(name string, store state.StateStore) {
	instance.stateStores[fmt.Sprintf("state.%s", name)] = store
}

func (s *stateStoreRegistry) CreateStateStore(name string) (state.StateStore, error) {
	for key, s := range s.stateStores {
		if key == name {
			return s, nil
		}
	}

	return nil, fmt.Errorf("couldn't find state store %s", name)
}
