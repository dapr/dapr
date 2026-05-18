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

package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxSchemaCacheEntries limits the number of tool schemas cached per server
	// to prevent memory exhaustion from a malicious MCP server.
	maxSchemaCacheEntries = 500

	// maxSchemaSize limits the size of a single tool's input schema (1 MB).
	maxSchemaSize = 1 << 20
)

// toolSchemaCache is a per-server cache of tool input schemas.
// Shared between ListTools (writes) and CallTool (reads) activities
// for the same server.
type toolSchemaCache struct {
	mu      sync.RWMutex
	schemas map[string]json.RawMessage // toolName -> inputSchema
}

func (c *toolSchemaCache) set(toolName string, schema json.RawMessage) error {
	if len(schema) > maxSchemaSize {
		return fmt.Errorf("schema for tool %q exceeds max size (%d > %d)", toolName, len(schema), maxSchemaSize)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.schemas == nil {
		c.schemas = make(map[string]json.RawMessage)
	}
	// Allow upsert (overwrite existing), but reject new entries past the limit.
	if _, exists := c.schemas[toolName]; !exists && len(c.schemas) >= maxSchemaCacheEntries {
		return fmt.Errorf("schema cache full (%d entries), cannot add tool %q", maxSchemaCacheEntries, toolName)
	}
	c.schemas[toolName] = schema
	return nil
}

func (c *toolSchemaCache) get(toolName string) (json.RawMessage, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.schemas[toolName]
	return s, ok
}

// toolListCache is a per-server cache of the full tool list returned by ListTools.
// Populated eagerly by RegisterMCPServer from the DiscoverTools result, then
// served to ListTools workflow invocations to avoid redundant upstream calls.
// Refreshed on hot-reload (RegisterMCPServer is called again).
type toolListCache struct {
	mu    sync.RWMutex
	tools []*mcp.Tool
}

func (c *toolListCache) store(tools []*mcp.Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = tools
}

func (c *toolListCache) load() ([]*mcp.Tool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tools, len(c.tools) > 0
}

// validateToolArguments performs client-side validation of tool arguments
// against the tool's declared input schema (if known).
// It checks that all required properties are present.
// Returns an empty string when validation passes or no schema is available,
// or an error message describing the missing/invalid fields.
func validateToolArguments(schemas *toolSchemaCache, toolName string, args map[string]any) string {
	raw, ok := schemas.get(toolName)
	if !ok || raw == nil {
		return ""
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return ""
	}

	requiredRaw, ok := schema[jsonFieldRequired]
	if !ok {
		return ""
	}

	requiredList, ok := requiredRaw.([]any)
	if !ok {
		return ""
	}

	var missing []string
	for _, r := range requiredList {
		name, ok := r.(string)
		if !ok {
			continue
		}
		if _, present := args[name]; !present {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return fmt.Sprintf("call-tool: missing required argument(s) for tool %q: %s",
			toolName, strings.Join(missing, ", "))
	}
	return ""
}
