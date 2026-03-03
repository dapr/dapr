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

import resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"

func (c *ComponentStore) GetResiliencyResource(name string) (resiliencyapi.Resiliency, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, res := range c.resiliencyResources {
		if res.ObjectMeta.Name == name {
			return c.resiliencyResources[i], true
		}
	}
	return resiliencyapi.Resiliency{}, false
}

func (c *ComponentStore) AddResiliencyResource(res resiliencyapi.Resiliency) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, r := range c.resiliencyResources {
		if r.ObjectMeta.Name == res.Name {
			c.resiliencyResources[i] = res
			return
		}
	}

	c.resiliencyResources = append(c.resiliencyResources, res)
}

func (c *ComponentStore) ListResiliencyResources() []resiliencyapi.Resiliency {
	c.lock.RLock()
	defer c.lock.RUnlock()
	resources := make([]resiliencyapi.Resiliency, len(c.resiliencyResources))
	copy(resources, c.resiliencyResources)
	return resources
}

func (c *ComponentStore) DeleteResiliencyResource(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, res := range c.resiliencyResources {
		if res.ObjectMeta.Name == name {
			c.resiliencyResources = append(c.resiliencyResources[:i], c.resiliencyResources[i+1:]...)
			return
		}
	}
}
