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
	"fmt"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcp "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

// Executor wraps a task.TaskExecutor and exposes methods to register
// in-process workflow subsystems after resources are loaded.
type Executor struct {
	registry *task.TaskRegistry
	executor backend.Executor
}

// NewExecutor creates an in-process executor with an empty registry.
// Call RegisterMCP after MCPServers are loaded.
func NewExecutor() *Executor {
	registry := task.NewTaskRegistry()
	return &Executor{
		registry: registry,
		executor: task.NewTaskExecutor(registry),
	}
}

// Backend returns the underlying backend.Executor for use by the workflow engine.
func (e *Executor) Backend() backend.Executor {
	return e.executor
}

// RegisterMCP registers versioned MCP workflows for all known MCPServers.
// Must be called after MCPServers are loaded into the component store.
func (e *Executor) RegisterMCP(store *compstore.ComponentStore, sec security.Handler) error {
	if err := mcp.RegisterMCP(e.registry, mcp.Options{
		Store:    store,
		Security: sec,
	}); err != nil {
		return fmt.Errorf("failed to register MCP in-process workflows: %w", err)
	}
	return nil
}

// RegisterMCPServer registers workflows for a single MCPServer.
// Called on hot-reload when a new server is added.
func (e *Executor) RegisterMCPServer(server mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) error {
	return mcp.RegisterMCPServer(e.registry, server, mcp.Options{
		Store:    store,
		Security: sec,
	})
}
