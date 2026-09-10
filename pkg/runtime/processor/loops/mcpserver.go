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

package loops

import (
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
)

// AddMCPServer adds an MCP server. Routed by the root loop to the per-name
// MCPServer loop so that all events for a given name serialise in submission
// order. The event satisfies both EventRoot (so callers can enqueue at the
// root) and EventMCPServer (so the per-name loop can pull it off its queue).
type AddMCPServer struct {
	*rootbase
	*mcpbase
	Server mcpserverapi.MCPServer
	Result chan<- error
}

// DeleteMCPServer removes an MCP server by name. Same routing rules as
// AddMCPServer: enqueued at the root, processed on the per-name loop, so
// Delete strictly follows any Adds that were enqueued before it.
type DeleteMCPServer struct {
	*rootbase
	*mcpbase
	Name string
	Done chan struct{}
}

// MCPServerRegistered is the per-name MCPServer loop's notification back to
// the root loop that an Add has finished its slow path (secret resolution +
// compstore write + workflow registration). The root uses it to decrement its
// in-flight counter so Barrier completion can fire once every queued Add has
// drained.
type MCPServerRegistered struct {
	*rootbase
}
