/*
Copyright 2026 The Dapr Authors
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

	"github.com/dapr/components-contrib/vector"
)

func (c *ComponentStore) AddVector(name string, vector vector.Vector) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.vectors[name] = vector
}

func (c *ComponentStore) GetVector(name string) (vector.Vector, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	vector, ok := c.vectors[name]

	return vector, ok
}

func (c *ComponentStore) ListVectors() map[string]vector.Vector {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return maps.Clone(c.vectors)
}

func (c *ComponentStore) DeleteVector(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.vectors, name)
}

func (c *ComponentStore) VectorsLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.vectors)
}
