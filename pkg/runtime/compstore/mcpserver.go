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
	mcpserverv1alpha1 "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
)

// GetMCPServer returns the MCPServer with the given name, if it exists.
func (c *ComponentStore) GetMCPServer(name string) (mcpserverv1alpha1.MCPServer, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	for i, s := range c.mcpServers {
		if s.Name == name {
			return c.mcpServers[i], true
		}
	}
	return mcpserverv1alpha1.MCPServer{}, false
}

// AddMCPServer adds or replaces an MCPServer in the store.
func (c *ComponentStore) AddMCPServer(s mcpserverv1alpha1.MCPServer) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, existing := range c.mcpServers {
		if existing.Name == s.Name {
			c.mcpServers[i] = s
			return
		}
	}
	c.mcpServers = append(c.mcpServers, s)
}

// ListMCPServers returns a copy of all MCPServer resources in the store.
func (c *ComponentStore) ListMCPServers() []mcpserverv1alpha1.MCPServer {
	c.lock.RLock()
	defer c.lock.RUnlock()

	out := make([]mcpserverv1alpha1.MCPServer, len(c.mcpServers))
	copy(out, c.mcpServers)
	return out
}

// DeleteMCPServer removes the MCPServer with the given name from the store.
func (c *ComponentStore) DeleteMCPServer(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, s := range c.mcpServers {
		if s.Name == name {
			c.mcpServers = append(c.mcpServers[:i], c.mcpServers[i+1:]...)
			return
		}
	}
}
