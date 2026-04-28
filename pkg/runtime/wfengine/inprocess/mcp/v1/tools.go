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

	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxSchemaCacheEntries limits the number of tool schemas cached per server
	// to prevent memory exhaustion from a malicious MCP server.
	maxSchemaCacheEntries = 10_000

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

// convertCallToolResult converts an mcp.CallToolResult from the go-sdk into a proto CallMCPToolResponse.
// Each MCP content type maps to the corresponding oneof variant in MCPContentBlock.
// Unknown/future content types are JSON-marshaled into a text block as a forward-compatible fallback.
func convertCallToolResult(r *mcp.CallToolResult) *wfv1.CallMCPToolResponse {
	out := &wfv1.CallMCPToolResponse{IsError: r.IsError}
	for _, c := range r.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			out.Content = append(out.Content, &wfv1.MCPContentBlock{
				Content: &wfv1.MCPContentBlock_Text{
					Text: &wfv1.MCPTextContent{Text: v.Text},
				},
			})
		case *mcp.ImageContent:
			out.Content = append(out.Content, &wfv1.MCPContentBlock{
				Content: &wfv1.MCPContentBlock_Image{
					Image: &wfv1.MCPBinaryContent{MimeType: v.MIMEType, Data: v.Data},
				},
			})
		case *mcp.AudioContent:
			out.Content = append(out.Content, &wfv1.MCPContentBlock{
				Content: &wfv1.MCPContentBlock_Audio{
					Audio: &wfv1.MCPBinaryContent{MimeType: v.MIMEType, Data: v.Data},
				},
			})
		case *mcp.ResourceLink:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, &wfv1.MCPContentBlock{
					Content: &wfv1.MCPContentBlock_ResourceLink{
						ResourceLink: &wfv1.MCPResourceContent{Resource: raw},
					},
				})
			}
		case *mcp.EmbeddedResource:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, &wfv1.MCPContentBlock{
					Content: &wfv1.MCPContentBlock_EmbeddedResource{
						EmbeddedResource: &wfv1.MCPResourceContent{Resource: raw},
					},
				})
			}
		default:
			if b, err := json.Marshal(c); err == nil {
				out.Content = append(out.Content, &wfv1.MCPContentBlock{
					Content: &wfv1.MCPContentBlock_Text{
						Text: &wfv1.MCPTextContent{Text: string(b)},
					},
				})
			}
		}
	}
	return out
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
