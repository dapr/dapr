/*
Copyright 2023 The Dapr Authors
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

package compstore

import (
	"fmt"

	"github.com/dapr/components-contrib/state"
)

func (c *ComponentStore) AddStateStore(name string, store state.Store) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.states[name] = store
}

func (c *ComponentStore) AddStateStoreActor(name string, store state.Store) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.actorStateStore.store != nil && c.actorStateStore.name != name {
		return fmt.Errorf("detected duplicate actor state store: %s and %s", c.actorStateStore.name, name)
	}
	c.states[name] = store
	c.actorStateStore.name = name
	c.actorStateStore.store = store
	return nil
}

func (c *ComponentStore) GetStateStore(name string) (state.Store, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	store, ok := c.states[name]
	return store, ok
}

func (c *ComponentStore) ListStateStores() map[string]state.Store {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.states
}

func (c *ComponentStore) DeleteStateStore(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.actorStateStore.name == name {
		c.actorStateStore.name = ""
		c.actorStateStore.store = nil
	}
	delete(c.states, name)
}

func (c *ComponentStore) StateStoresLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.states)
}

func (c *ComponentStore) GetStateStoreActor() (state.Store, string, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	if c.actorStateStore.store == nil {
		return nil, "", false
	}
	return c.actorStateStore.store, c.actorStateStore.name, true
}
