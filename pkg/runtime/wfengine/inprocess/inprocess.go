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
// gRPC. Subsystems register their orchestrators and activities via Populate
// functions (e.g. mcp.Populate) at startup.
package inprocess

import (
	"fmt"

	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

// Options configures all in-process workflow subsystems.
type Options struct {
	ComponentStore *compstore.ComponentStore
	Security       security.Handler
}

// NewExecutor creates a task.TaskExecutor backed by a registry that is
// eagerly populated with all known in-process workflow subsystems.
// Therefore, workflows handle missing resources gracefully at call time (e.g. "MCPServer not found").
func NewExecutor(opts Options) (backend.Executor, error) {
	registry := task.NewTaskRegistry()

	// MCP subsystem: wildcard orchestrator + transport activities.
	if err := mcp.RegisterMCP(registry, mcp.Options{
		Store: opts.ComponentStore,
		JWT:   security.NewSPIFFEJWTFetcher(opts.Security),
	}); err != nil {
		return nil, fmt.Errorf("failed to register MCP in-process workflows: %w", err)
	}

	// Future in-process subsystems would register here.

	return task.NewTaskExecutor(registry), nil
}
