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
	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/dapr/pkg/config"
)

func (c *ComponentStore) AddConfiguration(name string, store configuration.Store) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.configurations[name] = store
}

func (c *ComponentStore) GetConfiguration(name string) (configuration.Store, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	store, ok := c.configurations[name]
	return store, ok
}

func (c *ComponentStore) ListConfigurations() map[string]configuration.Store {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.configurations
}

func (c *ComponentStore) ConfigurationsLen() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.configurations)
}

func (c *ComponentStore) DeleteConfiguration(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.configurations, name)
}

func (c *ComponentStore) AddConfigurationSubscribe(name string, ch chan struct{}) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.configurationSubscribes[name] = ch
}

func (c *ComponentStore) GetConfigurationSubscribe(name string) (chan struct{}, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	ch, ok := c.configurationSubscribes[name]
	return ch, ok
}

func (c *ComponentStore) DeleteConfigurationSubscribe(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	stop := c.configurationSubscribes[name]
	if stop != nil {
		close(stop)
	}
	delete(c.configurationSubscribes, name)
}

func (c *ComponentStore) DeleteAllConfigurationSubscribe() {
	c.lock.Lock()
	defer c.lock.Unlock()
	for name, stop := range c.configurationSubscribes {
		close(stop)
		delete(c.configurationSubscribes, name)
	}
}

func (c *ComponentStore) AddSecretsConfiguration(name string, secretsScope config.SecretsScope) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.secretsConfigurations[name] = secretsScope
}

func (c *ComponentStore) GetSecretsConfiguration(name string) (config.SecretsScope, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	secretsScope, ok := c.secretsConfigurations[name]
	return secretsScope, ok
}

func (c *ComponentStore) DeleteSecretsConfiguration(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.secretsConfigurations, name)
}
