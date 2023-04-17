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

import compsv1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"

func (c *ComponentStore) GetComponent(componentType, name string) (compsv1alpha1.Component, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, comp := range c.components {
		if comp.Spec.Type == componentType && comp.ObjectMeta.Name == name {
			return c.components[i], true
		}
	}
	return compsv1alpha1.Component{}, false
}

func (c *ComponentStore) AddComponent(component compsv1alpha1.Component) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, comp := range c.components {
		if comp.Spec.Type == component.Spec.Type && comp.ObjectMeta.Name == component.Name {
			c.components[i] = component
			return
		}
	}

	c.components = append(c.components, component)
}

func (c *ComponentStore) ListComponents() []compsv1alpha1.Component {
	c.lock.RLock()
	defer c.lock.RUnlock()
	comps := make([]compsv1alpha1.Component, len(c.components))
	copy(comps, c.components)
	return comps
}

func (c *ComponentStore) DeleteComponent(compType, name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, comp := range c.components {
		if comp.Spec.Type == compType && comp.ObjectMeta.Name == name {
			c.components = append(c.components[:i], c.components[i+1:]...)
			return
		}
	}
}
