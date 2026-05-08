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

package inprocess

import (
	"context"
	"fmt"
	"sync"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcp "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1"
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/durabletask-go/task"
)

// mcpEntry holds the registration state for a single MCPServer. Its own mutex
// serializes Register/Unregister for that one server name; distinct entries
// run in parallel.
type mcpEntry struct {
	mu     sync.Mutex
	holder *mcp.SessionHolder
	tools  []string // tool names for per-tool CallTool workflow unregistration
}

// mcpRegistry manages per-MCPServer registration state inside the shared task
// registry. Each MCPServer gets one mcpEntry; the entry's mutex serializes
// Register/Unregister for that name while distinct names run in parallel.
type mcpRegistry struct {
	entriesMu sync.RWMutex
	entries   map[string]*mcpEntry
	registry  *task.TaskRegistry
}

func newMCPRegistry(registry *task.TaskRegistry) *mcpRegistry {
	return &mcpRegistry{
		entries:  make(map[string]*mcpEntry),
		registry: registry,
	}
}

// entry returns the mcpEntry for name, creating one if it doesn't exist.
// Entries are never removed once created — they persist for the lifetime of
// the registry so the per-server mutex stays stable across re-registration.
func (r *mcpRegistry) entry(name string) *mcpEntry {
	r.entriesMu.RLock()
	existing, ok := r.entries[name]
	r.entriesMu.RUnlock()
	if ok {
		return existing
	}

	r.entriesMu.Lock()
	defer r.entriesMu.Unlock()
	if existing, ok := r.entries[name]; ok {
		return existing
	}
	created := &mcpEntry{}
	r.entries[name] = created
	return created
}

// register registers workflows for a single MCPServer.
// Eagerly connects and calls ListTools to discover tool names, then registers
// per-tool CallTool workflows for fine-grained observability.
// Called by the processor on initial load and hot-reload (same path).
//
// Concurrency: the per-server entry mutex serializes registrations for the
// same server name. Distinct server names use distinct entries, so their
// network IO and workflow registration run in parallel.
func (r *mcpRegistry) register(ctx context.Context, server mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) error {
	entry := r.entry(server.Name)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Tear down previous registration (initial load: no-op; hot-reload: closes
	// the prior session and removes its per-tool CallTool workflows).
	if entry.holder != nil {
		entry.holder.Close()
		entry.holder = nil
	}
	for _, tool := range entry.tools {
		r.registry.RemoveVersionedWorkflow(mcpnames.MCPCallToolWorkflowName(server.Name, tool))
	}
	entry.tools = nil

	connectCtx, cancel := context.WithTimeout(ctx, mcp.CallTimeout(&server))
	defer cancel()

	holder, err := mcp.NewSessionHolder(connectCtx, &server, store, sec)
	if err != nil {
		return fmt.Errorf("MCPServer %q: failed to connect: %w", server.Name, err)
	}

	// Discover tools and register workflows. If discovery fails, we cannot
	// register a useful MCPServer — fail the entire registration.
	toolNames, err := mcp.RegisterMCPServer(mcp.RegisterOptions{
		Ctx:      connectCtx,
		Registry: r.registry,
		Holder:   holder,
		Server:   server,
		Store:    store,
		Security: sec,
	})
	if err != nil {
		holder.Close()
		return err
	}

	entry.holder = holder
	entry.tools = toolNames
	return nil
}

// closeAll closes every holder.
func (r *mcpRegistry) closeAll() {
	r.entriesMu.Lock()
	entries := make([]*mcpEntry, 0, len(r.entries))
	for _, e := range r.entries {
		entries = append(entries, e)
	}
	r.entriesMu.Unlock()

	for _, entry := range entries {
		if h := entry.holder; h != nil {
			h.Close()
		}
	}
}

// unregister removes workflows for a deleted MCPServer.
// Serializes against concurrent register/unregister for the same server name
// via the per-server entry mutex, but does not block other servers.
func (r *mcpRegistry) unregister(serverName string) {
	entry := r.entry(serverName)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.holder != nil {
		entry.holder.Close()
		entry.holder = nil
	}
	for _, tool := range entry.tools {
		r.registry.RemoveVersionedWorkflow(mcpnames.MCPCallToolWorkflowName(serverName, tool))
	}
	entry.tools = nil

	// Remove ListTools + activities.
	mcp.UnregisterMCPServer(r.registry, serverName)
}
