package state

import (
	"fmt"
	"sync"
)

type StateStoreRegistry interface {
	CreateStateStore(name string) (StateStore, error)
}

type stateStoreRegistry struct {
	stateStores map[string]StateStore
}

var instance *stateStoreRegistry
var once sync.Once

func NewStateStoreRegistry() StateStoreRegistry {
	once.Do(func() {
		instance = &stateStoreRegistry{
			stateStores: map[string]StateStore{},
		}
	})
	return instance
}

func RegisterStateStore(name string, store StateStore) {
	instance.stateStores[fmt.Sprintf("state.%s", name)] = store
}

func (s *stateStoreRegistry) CreateStateStore(name string) (StateStore, error) {
	for key, s := range s.stateStores {
		if key == name {
			return s, nil
		}
	}

	return nil, fmt.Errorf("couldn't find state store %s", name)
}
