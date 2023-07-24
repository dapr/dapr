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

import "github.com/dapr/components-contrib/secretstores"

func (c *ComponentStore) AddSecretStore(name string, store secretstores.SecretStore) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.secrets[name] = store
}

func (c *ComponentStore) GetSecretStore(name string) (secretstores.SecretStore, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	store, ok := c.secrets[name]
	return store, ok
}

func (c *ComponentStore) ListSecretStores() map[string]secretstores.SecretStore {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.secrets
}

func (c *ComponentStore) DeleteSecretStore(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.secrets, name)
}

func (c *ComponentStore) SecretStoresLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.secrets)
}
