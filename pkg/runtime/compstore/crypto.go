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
	"github.com/dapr/components-contrib/crypto"
)

func (c *ComponentStore) AddCryptoProvider(name string, provider crypto.SubtleCrypto) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.cryptoProviders[name] = provider
}

func (c *ComponentStore) GetCryptoProvider(name string) (crypto.SubtleCrypto, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	provider, ok := c.cryptoProviders[name]
	return provider, ok
}

func (c *ComponentStore) ListCryptoProviders() map[string]crypto.SubtleCrypto {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.cryptoProviders
}

func (c *ComponentStore) DeleteCryptoProvider(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.cryptoProviders, name)
}

func (c *ComponentStore) CryptoProvidersLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.cryptoProviders)
}
