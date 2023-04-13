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

import "github.com/dapr/components-contrib/bindings"

func (c *ComponentStore) AddInputBinding(name string, binding bindings.InputBinding) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.inputBindings[name] = binding
}

func (c *ComponentStore) GetInputBinding(name string) (bindings.InputBinding, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	binding, ok := c.inputBindings[name]
	return binding, ok
}

func (c *ComponentStore) ListInputBindings() map[string]bindings.InputBinding {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.inputBindings
}

func (c *ComponentStore) DeleteInputBinding(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.inputBindings, name)
}

func (c *ComponentStore) AddInputBindingRoute(name, route string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.inputBindingRoutes[name] = route
}

func (c *ComponentStore) GetInputBindingRoute(name string) (string, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	route, ok := c.inputBindingRoutes[name]
	return route, ok
}

func (c *ComponentStore) ListInputBindingRoutes() map[string]string {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.inputBindingRoutes
}

func (c *ComponentStore) DeleteInputBindingRoute(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.inputBindingRoutes, name)
}

func (c *ComponentStore) AddOutputBinding(name string, binding bindings.OutputBinding) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.outputBindings[name] = binding
}

func (c *ComponentStore) GetOutputBinding(name string) (bindings.OutputBinding, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	binding, ok := c.outputBindings[name]
	return binding, ok
}

func (c *ComponentStore) ListOutputBindings() map[string]bindings.OutputBinding {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.outputBindings
}

func (c *ComponentStore) DeleteOutputBinding(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.outputBindings, name)
}
