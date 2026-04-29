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

// Package inprocess creates and configures the in-process workflow executor
// used for dapr-internal workflows (e.g. MCP tool calls). The executor runs
// alongside the sidecar rather than dispatching work to an external SDK via
// gRPC. Subsystems register their orchestrators and activities after resources
// are loaded (e.g. MCPServers) to ensure the store is populated.
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
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

// Executor wraps a task.TaskExecutor and exposes methods to register
// in-process workflow subsystems after resources are loaded.
type Executor struct {
	mu        sync.Mutex
	registry  *task.TaskRegistry
	executor  backend.Executor
	mcpHolder map[string]*mcp.SessionHolder
	mcpTools  map[string][]string // serverName → []toolName for per-tool unregistration
}

// NewExecutor creates an in-process executor with an empty registry.
// Workflows are registered per-resource via RegisterMCPServer.
func NewExecutor() *Executor {
	registry := task.NewTaskRegistry()
	return &Executor{
		registry:  registry,
		executor:  task.NewTaskExecutor(registry),
		mcpHolder: make(map[string]*mcp.SessionHolder),
		mcpTools:  make(map[string][]string),
	}
}

// Backend returns the underlying backend.Executor for use by the workflow engine.
func (e *Executor) Backend() backend.Executor {
	return e.executor
}

// RegisterMCPServer registers workflows for a single MCPServer.
// Eagerly connects and calls ListTools to discover tool names, then registers
// per-tool CallTool workflows for fine-grained observability.
// Called by the processor on initial load and hot-reload (same path).
// Thread-safe — acquires the executor lock for the entire operation.
func (e *Executor) RegisterMCPServer(ctx context.Context, server mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Close old holder and unregister old per-tool workflows if hot-reloading.
	if old, ok := e.mcpHolder[server.Name]; ok {
		old.Close()
		delete(e.mcpHolder, server.Name)
	}
	if oldTools, ok := e.mcpTools[server.Name]; ok {
		for _, tool := range oldTools {
			e.registry.RemoveVersionedWorkflow(mcpnames.MCPCallToolWorkflowName(server.Name, tool))
		}
		delete(e.mcpTools, server.Name)
	}

	// Create new session with timeout.
	connectCtx, cancel := context.WithTimeout(ctx, mcp.CallTimeout(&server))
	defer cancel()

	holder, err := mcp.NewSessionHolder(connectCtx, &server, store, sec)
	if err != nil {
		return fmt.Errorf("MCPServer %q: failed to connect: %w", server.Name, err)
	}

	// Discover tools and register workflows. If discovery fails, we cannot
	// register a useful MCPServer — fail the entire registration.
	toolNames, err := mcp.RegisterMCPServer(connectCtx, e.registry, holder, server, mcp.Options{
		Store:    store,
		Security: sec,
	})
	if err != nil {
		holder.Close()
		return err
	}

	e.mcpHolder[server.Name] = holder
	e.mcpTools[server.Name] = toolNames
	return nil
}

// UnregisterMCPServer removes workflows for a deleted MCPServer.
// Thread-safe — acquires the executor lock.
func (e *Executor) UnregisterMCPServer(serverName string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if old, ok := e.mcpHolder[serverName]; ok {
		old.Close()
		delete(e.mcpHolder, serverName)
	}

	// Remove per-tool CallTool workflows.
	if tools, ok := e.mcpTools[serverName]; ok {
		for _, tool := range tools {
			e.registry.RemoveVersionedWorkflow(mcpnames.MCPCallToolWorkflowName(serverName, tool))
		}
		delete(e.mcpTools, serverName)
	}

	// Remove ListTools + activities.
	mcp.UnregisterMCPServer(e.registry, serverName)
}
