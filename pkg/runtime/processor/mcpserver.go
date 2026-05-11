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

package processor

import (
	"context"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	"github.com/dapr/dapr/pkg/runtime/wfengine/wfregistrar"
)

// SetInProcessWorkflows installs the in-process workflow wfregistrar. Used by
// the root loop's MCPServer add path to register workflows backing managed
// MCP resources.
func (p *Processor) SetInProcessWorkflows(r wfregistrar.Registrar) {
	p.inProcessWorkflowsLock.Lock()
	defer p.inProcessWorkflowsLock.Unlock()
	p.inProcessWorkflows = r
}

// getInProcessWorkflows returns the wfregistrar, or nil if it has not been
// set yet.
func (p *Processor) getInProcessWorkflows() wfregistrar.Registrar {
	p.inProcessWorkflowsLock.RLock()
	defer p.inProcessWorkflowsLock.RUnlock()
	return p.inProcessWorkflows
}

// AddPendingMCPServer enqueues an MCP server and returns a result chan.
func (p *Processor) AddPendingMCPServer(ctx context.Context, s mcpserverapi.MCPServer) <-chan error {
	if p.closed.Load() {
		return nil
	}
	res := make(chan error, 1)
	p.rootLoop.Loop().Enqueue(&loops.AddMCPServer{Server: s, Result: res})
	return res
}

// DeleteMCPServer removes an MCP server from the compstore and unregisters
// its in-process workflows. Routed through the root loop so the delete is
// serialised against concurrent Add events for the same name. Returns when
// the delete has been applied, when ctx is cancelled, or immediately if the
// processor is already shut down.
func (p *Processor) DeleteMCPServer(ctx context.Context, name string) {
	if p.closed.Load() {
		return
	}
	done := make(chan struct{})
	p.rootLoop.Loop().Enqueue(&loops.DeleteMCPServer{Name: name, Done: done})
	select {
	case <-done:
	case <-ctx.Done():
	}
}
