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

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

// Executor wraps a task.TaskExecutor and exposes per-subsystem registrars for
// in-process workflows that run inside the sidecar. Subsystems (MCP today,
// others later) live in their own files; the Executor is a thin shell that
// holds the shared task registry and delegates to each subsystem registrar.
type Executor struct {
	registry *task.TaskRegistry
	executor backend.Executor
	mcp      *mcpRegistry
}

// NewExecutor creates an in-process executor with an empty registry.
// Workflows are registered per-resource via subsystem-specific methods
// (e.g. RegisterMCPServer).
func NewExecutor() *Executor {
	registry := task.NewTaskRegistry()
	return &Executor{
		registry: registry,
		executor: task.NewTaskExecutor(registry),
		mcp:      newMCPRegistry(registry),
	}
}

// Backend returns the underlying backend.Executor for use by the workflow engine.
func (e *Executor) Backend() backend.Executor {
	return e.executor
}

// RegisterMCPServer eagerly connects to the MCPServer, discovers its tools,
// and installs per-tool CallTool workflows plus a ListTools workflow into the
// shared task registry. Called by the processor on initial load and hot-reload.
func (e *Executor) RegisterMCPServer(ctx context.Context, server mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) error {
	return e.mcp.register(ctx, server, store, sec)
}

// UnregisterMCPServer removes the workflows for a deleted MCPServer.
func (e *Executor) UnregisterMCPServer(serverName string) {
	e.mcp.unregister(serverName)
}
