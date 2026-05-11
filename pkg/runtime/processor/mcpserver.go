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

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	"github.com/dapr/dapr/pkg/runtime/wfengine/wfregistrar"
)

// mcpStdioEnvResource is a thin adapter that wraps an MCPServer and overrides
// NameValuePairs to return Spec.Endpoint.Stdio.Env instead of the HTTP transport headers.
// This lets ProcessResource resolve secretKeyRef and envRef entries in stdio env via the
// same secret-store infrastructure used for HTTP headers.
type mcpStdioEnvResource struct {
	*mcpserverapi.MCPServer
}

func (r mcpStdioEnvResource) NameValuePairs() []commonapi.NameValuePair {
	if r.Spec.Endpoint.Stdio == nil {
		return nil
	}
	return r.Spec.Endpoint.Stdio.Env
}

// ProcessMCPServerSecrets resolves secretKeyRef and envRef entries in the
// transport headers (spec.endpoint.streamableHTTP.headers or spec.endpoint.sse.headers)
// and spec.endpoint.stdio.env using the configured secret store.
// Unlike components, MCPServer resources load after all secret store components are initialized,
// so secrets are available immediately.
// The underlying p.secret.ProcessResource (the secret manager) logs errors
// internally and resolves what it can; it does not return an error. Unresolvable
// secretKeyRef values remain as empty strings.
//
// This does NOT resolve auth.oauth2.secretKeyRef: the OAuth2 client secret is
// fetched at connection time by the MCP worker (see pkg/runtime/mcp/auth) and is
// never written into the spec, so it does not take part in the hot-reload diff.
//
// The hot-reload reconciler also calls this on a copy of an incoming spec before
// comparing it against the already-resolved stored copy, so an unchanged
// secret-ref server is not needlessly reloaded while a rotated secret value
// still triggers a reload.
func (p *Processor) ProcessMCPServerSecrets(ctx context.Context, s *mcpserverapi.MCPServer) {
	// Resolve transport headers (envRef + secretKeyRef).
	p.secret.ProcessResource(ctx, s)

	// Resolve spec.endpoint.stdio.env
	if s.Spec.Endpoint.Stdio != nil {
		p.secret.ProcessResource(ctx, mcpStdioEnvResource{s})
	}
}

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
