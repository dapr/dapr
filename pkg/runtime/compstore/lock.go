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

import "github.com/dapr/components-contrib/lock"

func (c *ComponentStore) AddLock(name string, store lock.Store) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.locks[name] = store
}

func (c *ComponentStore) GetLock(name string) (lock.Store, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	store, ok := c.locks[name]
	return store, ok
}

func (c *ComponentStore) ListLocks() map[string]lock.Store {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.locks
}

func (c *ComponentStore) DeleteLock(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.locks, name)
}

func (c *ComponentStore) LocksLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.locks)
}
