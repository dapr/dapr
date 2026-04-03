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
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.mcp")

// RoutingExecutor is a backend.Executor that intercepts dapr.mcp.* orchestrations,
// and dispatches them to the built-in in-process MCP executor.
// All other names are forwarded to the underlying gRPC executor (grpcExecutor).
//
// The MCP executor is installed lazily via EnableMCP, which is called by
// ActivateMCPServers when the first MCPServer manifest is loaded. Until then,
// dapr.mcp.* requests return an error and no MCP-related allocations are made.
type RoutingExecutor struct {
	grpc backend.Executor
	mcp  atomic.Pointer[backend.Executor]
}

// NewRoutingExecutor returns a new RoutingExecutor wrapping the provided gRPC
// executor. The MCP executor is not yet installed; call EnableMCP to activate it.
func NewRoutingExecutor(grpcExec backend.Executor) *RoutingExecutor {
	return &RoutingExecutor{grpc: grpcExec}
}

// EnableMCP installs the built-in MCP executor.
// It is called by ActivateMCPServers the first time MCPServer manifests are loaded.
// Subsequent calls are no-ops (the pointer is only stored once).
func (r *RoutingExecutor) EnableMCP(mcpExec backend.Executor) {
	r.mcp.CompareAndSwap(nil, &mcpExec)
}

// ExecuteOrchestrator implements backend.Executor.
// Orchestrations whose name starts with "dapr.mcp." are routed to the in-process MCP executor;
// all others are forwarded to the gRPC executor.
func (r *RoutingExecutor) ExecuteOrchestrator(
	ctx context.Context,
	iid api.InstanceID,
	oldEvents []*protos.HistoryEvent,
	newEvents []*protos.HistoryEvent,
) (*protos.OrchestratorResponse, error) {
	if isMCPOrchestration(oldEvents, newEvents) {
		p := r.mcp.Load()
		if p == nil {
			return nil, fmt.Errorf("dapr.mcp.* orchestration received but no MCPServer resources are loaded")
		}
		return (*p).ExecuteOrchestrator(ctx, iid, oldEvents, newEvents)
	}
	return r.grpc.ExecuteOrchestrator(ctx, iid, oldEvents, newEvents)
}

// ExecuteActivity implements backend.Executor.
// Activity names starting with "dapr-mcp-" are dispatched to the in-process MCP executor,
// and all others go to the gRPC executor.
func (r *RoutingExecutor) ExecuteActivity(
	ctx context.Context,
	iid api.InstanceID,
	event *protos.HistoryEvent,
) (*protos.HistoryEvent, error) {
	if isMCPActivity(event) {
		p := r.mcp.Load()
		if p == nil {
			return nil, fmt.Errorf("dapr-mcp-* activity received but no MCPServer resources are loaded")
		}
		return (*p).ExecuteActivity(ctx, iid, event)
	}
	return r.grpc.ExecuteActivity(ctx, iid, event)
}

// Shutdown implements backend.Executor. Both sub-executors are shut down.
// Errors from the MCP executor are logged,
// but do not prevent the gRPC executor from shutting down.
func (r *RoutingExecutor) Shutdown(ctx context.Context) error {
	if p := r.mcp.Load(); p != nil {
		if err := (*p).Shutdown(ctx); err != nil {
			log.Warnf("error shutting down MCP executor: %s", err)
		}
	}
	return r.grpc.Shutdown(ctx)
}

// isMCPOrchestration returns true when the execution-started event in the
// provided event slices carries a name that begins with "dapr.mcp.".
// The execution-started event may live in oldEvents (replay) or newEvents (first execution).
func isMCPOrchestration(oldEvents, newEvents []*protos.HistoryEvent) bool {
	name := orchestrationName(oldEvents, newEvents)
	return strings.HasPrefix(name, orchestrationNamePrefix)
}

// orchestrationName extracts the orchestration name from the execution-started
// event in the provided history slices. Returns "" when no such event is found.
func orchestrationName(oldEvents, newEvents []*protos.HistoryEvent) string {
	for _, e := range oldEvents {
		if es := e.GetExecutionStarted(); es != nil {
			return es.GetName()
		}
	}
	for _, e := range newEvents {
		if es := e.GetExecutionStarted(); es != nil {
			return es.GetName()
		}
	}
	return ""
}

// isMCPActivity returns true when the task-scheduled event in e has a name beginning with "dapr-mcp-".
func isMCPActivity(e *protos.HistoryEvent) bool {
	if ts := e.GetTaskScheduled(); ts != nil {
		return strings.HasPrefix(ts.GetName(), "dapr-mcp-")
	}
	return false
}
