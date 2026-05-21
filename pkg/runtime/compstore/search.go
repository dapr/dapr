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

	"github.com/dapr/components-contrib/search"
)

func (c *ComponentStore) AddSearch(name string, search search.Search) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.searches[name] = search
}

func (c *ComponentStore) GetSearch(name string) (search.Search, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	search, ok := c.searches[name]

	return search, ok
}

func (c *ComponentStore) ListSearches() map[string]search.Search {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return maps.Clone(c.searches)
}

func (c *ComponentStore) DeleteSearch(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.searches, name)
}

func (c *ComponentStore) SearchesLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.searches)
}
