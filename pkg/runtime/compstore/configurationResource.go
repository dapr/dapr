/*
Copyright 2024 The Dapr Authors
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

import configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"

func (c *ComponentStore) GetConfigurationResource(name string) (configapi.Configuration, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, cfg := range c.configurationResources {
		if cfg.ObjectMeta.Name == name {
			return c.configurationResources[i], true
		}
	}
	return configapi.Configuration{}, false
}

func (c *ComponentStore) AddConfigurationResource(cfg configapi.Configuration) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, c2 := range c.configurationResources {
		if c2.ObjectMeta.Name == cfg.Name {
			c.configurationResources[i] = cfg
			return
		}
	}

	c.configurationResources = append(c.configurationResources, cfg)
}

func (c *ComponentStore) ListConfigurationResources() []configapi.Configuration {
	c.lock.RLock()
	defer c.lock.RUnlock()
	resources := make([]configapi.Configuration, len(c.configurationResources))
	copy(resources, c.configurationResources)
	return resources
}

func (c *ComponentStore) DeleteConfigurationResource(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, cfg := range c.configurationResources {
		if cfg.ObjectMeta.Name == name {
			c.configurationResources = append(c.configurationResources[:i], c.configurationResources[i+1:]...)
			return
		}
	}
}
