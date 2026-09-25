/*
Copyright 2025 The Dapr Authors
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
	"maps"

	"github.com/dapr/components-contrib/binarystore"
)

func (c *ComponentStore) AddBinaryStore(name string, store binarystore.BinaryStore) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.binaryStores[name] = store
}

func (c *ComponentStore) GetBinaryStore(name string) (binarystore.BinaryStore, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	store, ok := c.binaryStores[name]

	return store, ok
}

func (c *ComponentStore) ListBinaryStores() map[string]binarystore.BinaryStore {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return maps.Clone(c.binaryStores)
}

func (c *ComponentStore) DeleteBinaryStore(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.binaryStores, name)
}

func (c *ComponentStore) BinaryStoresLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.binaryStores)
}
