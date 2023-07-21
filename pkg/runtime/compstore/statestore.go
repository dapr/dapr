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

import "github.com/dapr/components-contrib/state"

func (c *ComponentStore) AddStateStore(name string, store state.Store) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.states[name] = store
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
	delete(c.states, name)
}

func (c *ComponentStore) StateStoresLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.states)
}
