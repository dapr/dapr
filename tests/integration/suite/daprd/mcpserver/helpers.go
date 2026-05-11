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

package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

// extractText returns the .Text of an MCP content block when it's a
// TextContent variant. Returns "" for any other variant.
func extractText(c mcp.Content) string {
	if tc, ok := c.(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}
