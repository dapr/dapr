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
	"encoding/json"
	"io"
	"net/http"

	mcpserverv1alpha1 "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
)

// MCPServerSnapshot holds all cached state for an MCPServer.
type MCPServerSnapshot struct {
	Server     mcpserverv1alpha1.MCPServer
	HTTPClient *http.Client
	Session    io.Closer
}

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

// GetMCPServerSnapshot returns the MCPServer config, cached HTTP client, and cached session.
func (c *ComponentStore) GetMCPServerSnapshot(name string) (MCPServerSnapshot, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	for i, s := range c.mcpServers {
		if s.Name == name {
			snap := MCPServerSnapshot{Server: c.mcpServers[i]}
			if c.mcpHTTPClients != nil {
				snap.HTTPClient = c.mcpHTTPClients[name]
			}
			if c.mcpSessions != nil {
				snap.Session = c.mcpSessions[name]
			}
			return snap, true
		}
	}
	return MCPServerSnapshot{}, false
}

// AddMCPServer adds or replaces an MCPServer in the store.
// On replace, cached HTTP clients and sessions are invalidated since the
// server config (URL, auth, etc.) may have changed.
func (c *ComponentStore) AddMCPServer(s mcpserverv1alpha1.MCPServer) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, existing := range c.mcpServers {
		if existing.Name == s.Name {
			c.mcpServers[i] = s
			// Invalidate cached clients/sessions — config may have changed.
			delete(c.mcpHTTPClients, s.Name)
			if sess, ok := c.mcpSessions[s.Name]; ok {
				sess.Close()
				delete(c.mcpSessions, s.Name)
			}
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
// Closes any cached MCP session for the server.
func (c *ComponentStore) DeleteMCPServer(name string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	for i, s := range c.mcpServers {
		if s.Name == name {
			c.mcpServers = append(c.mcpServers[:i], c.mcpServers[i+1:]...)
			break
		}
	}
	delete(c.mcpToolSchemas, name)
	delete(c.mcpHTTPClients, name)
	if sess, ok := c.mcpSessions[name]; ok {
		sess.Close()
		delete(c.mcpSessions, name)
	}
}

// SetMCPToolSchema caches the raw JSON input schema for a tool on a given MCPServer.
// Called after a successful ListTools to enable client-side argument validation.
func (c *ComponentStore) SetMCPToolSchema(serverName, toolName string, schema json.RawMessage) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.mcpToolSchemas == nil {
		c.mcpToolSchemas = make(map[string]map[string]json.RawMessage)
	}
	if c.mcpToolSchemas[serverName] == nil {
		c.mcpToolSchemas[serverName] = make(map[string]json.RawMessage)
	}
	c.mcpToolSchemas[serverName][toolName] = schema
}

// GetMCPToolSchema returns the cached raw JSON input schema for a tool, if available.
func (c *ComponentStore) GetMCPToolSchema(serverName, toolName string) (json.RawMessage, bool) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if c.mcpToolSchemas == nil {
		return nil, false
	}
	tools, ok := c.mcpToolSchemas[serverName]
	if !ok {
		return nil, false
	}
	schema, ok := tools[toolName]
	return schema, ok
}

// SetMCPHTTPClient caches an HTTP client for the given MCPServer.
// The cached client is removed when the MCPServer is deleted.
func (c *ComponentStore) SetMCPHTTPClient(serverName string, client *http.Client) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.mcpHTTPClients == nil {
		c.mcpHTTPClients = make(map[string]*http.Client)
	}
	c.mcpHTTPClients[serverName] = client
}

// GetMCPHTTPClient returns the cached HTTP client for the given MCPServer,
// or nil if none is cached.
func (c *ComponentStore) GetMCPHTTPClient(serverName string) *http.Client {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if c.mcpHTTPClients == nil {
		return nil
	}
	return c.mcpHTTPClients[serverName]
}

// SetMCPSession caches an MCP session for the given MCPServer.
// The session is closed and removed when the MCPServer is deleted (hot-reload).
func (c *ComponentStore) SetMCPSession(serverName string, session io.Closer) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.mcpSessions == nil {
		c.mcpSessions = make(map[string]io.Closer)
	}
	// Close any existing session before replacing.
	if old, ok := c.mcpSessions[serverName]; ok {
		old.Close()
	}
	c.mcpSessions[serverName] = session
}

// GetMCPSession returns the cached MCP session for the given MCPServer,
// or nil if none is cached.
func (c *ComponentStore) GetMCPSession(serverName string) io.Closer {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if c.mcpSessions == nil {
		return nil
	}
	return c.mcpSessions[serverName]
}

// GetOrSetMCPSession atomically returns an existing cached session or stores
// the provided one. Returns the session that ended up in the cache (which may
// be a previously cached session if another goroutine won the race) and true
// if the provided session was stored (false if an existing one was returned).
func (c *ComponentStore) GetOrSetMCPSession(serverName string, session io.Closer) (io.Closer, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.mcpSessions == nil {
		c.mcpSessions = make(map[string]io.Closer)
	}
	if existing, ok := c.mcpSessions[serverName]; ok {
		return existing, false
	}
	c.mcpSessions[serverName] = session
	return session, true
}

// DeleteMCPSession closes and removes the cached MCP session for the given MCPServer.
func (c *ComponentStore) DeleteMCPSession(serverName string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if sess, ok := c.mcpSessions[serverName]; ok {
		sess.Close()
		delete(c.mcpSessions, serverName)
	}
}
