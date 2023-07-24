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

import httpEndpointv1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"

func (c *ComponentStore) GetHTTPEndpoint(name string) (httpEndpointv1alpha1.HTTPEndpoint, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for i, endpoint := range c.httpEndpoints {
		if endpoint.ObjectMeta.Name == name {
			return c.httpEndpoints[i], true
		}
	}
	return httpEndpointv1alpha1.HTTPEndpoint{}, false
}

func (c *ComponentStore) AddHTTPEndpoint(httpEndpoint httpEndpointv1alpha1.HTTPEndpoint) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, endpoint := range c.httpEndpoints {
		if endpoint.ObjectMeta.Name == httpEndpoint.Name {
			c.httpEndpoints[i] = httpEndpoint
			return
		}
	}

	c.httpEndpoints = append(c.httpEndpoints, httpEndpoint)
}

func (c *ComponentStore) ListHTTPEndpoints() []httpEndpointv1alpha1.HTTPEndpoint {
	c.lock.RLock()
	defer c.lock.RUnlock()
	endpoints := make([]httpEndpointv1alpha1.HTTPEndpoint, len(c.httpEndpoints))
	copy(endpoints, c.httpEndpoints)
	return endpoints
}

func (c *ComponentStore) DeleteHTTPEndpoint(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, endpoint := range c.httpEndpoints {
		if endpoint.ObjectMeta.Name == name {
			c.httpEndpoints = append(c.httpEndpoints[:i], c.httpEndpoints[i+1:]...)
			return
		}
	}
}
