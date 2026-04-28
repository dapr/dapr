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
	"github.com/dapr/dapr/pkg/internal/loader/validate"
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

// SetInternalWorkflows sets the in-process executor used to register internal
// workflows when resources are loaded or hot-reloaded.
func (p *Processor) SetInternalWorkflows(exec internalWorkflowRegistrar) {
	p.internalWorkflows = exec
}

// AddPendingMCPServer enqueues an MCPServer for processing.
// Returns false if the processor has shut down or the context is done.
func (p *Processor) AddPendingMCPServer(ctx context.Context, s mcpserverapi.MCPServer) bool {
	p.chlock.RLock()
	defer p.chlock.RUnlock()

	if p.shutdown.Load() {
		return false
	}

	select {
	case <-ctx.Done():
		return false
	case <-p.closedCh:
		return false
	case p.pendingMCPServers <- s:
		return true
	}
}

// processMCPServers reads from the pendingMCPServers channel, resolves secrets,
// and adds each MCPServer to the component store.
func (p *Processor) processMCPServers(ctx context.Context) error {
	for s := range p.pendingMCPServers {
		if s.Name == "" {
			continue
		}

		if err := validate.MCPServer(ctx, &s); err != nil {
			log.Warnf("MCPServer %q failed validation: %s", s.Name, err)
			continue
		}

		if err := validate.MCPServerSecurity(&s, p.kubernetesMode); err != nil {
			log.Warnf("MCPServer %q failed security validation: %s", s.Name, err)
			continue
		}

		p.processMCPServerSecrets(ctx, &s)
		p.compStore.AddMCPServer(s)
		log.Infof("MCPServer loaded: %s", s.LogName())

		if p.internalWorkflows != nil {
			if err := p.internalWorkflows.RegisterMCPServer(s, p.compStore, p.security); err != nil {
				log.Warnf("MCPServer %q: failed to register workflows: %s", s.Name, err)
			}
		}
	}

	return nil
}

// DeleteMCPServer removes an MCPServer from the store and unregisters its workflows.
func (p *Processor) DeleteMCPServer(serverName string) {
	p.compStore.DeleteMCPServer(serverName)
	if p.internalWorkflows != nil {
		p.internalWorkflows.UnregisterMCPServer(serverName)
	}
}

// processMCPServerSecrets resolves secretKeyRef and envRef entries in the
// transport headers (spec.endpoint.streamableHTTP.headers or spec.endpoint.sse.headers)
// and spec.endpoint.stdio.env using the configured secret store.
// Unlike components, MCPServer resources load after all secret store components are initialized,
// so secrets are available immediately.
// ProcessResource logs errors internally and resolves what it can; it does not
// return an error. Unresolvable secretKeyRef values remain as empty strings.
func (p *Processor) processMCPServerSecrets(ctx context.Context, s *mcpserverapi.MCPServer) {
	// Resolve transport headers (envRef + secretKeyRef).
	p.secret.ProcessResource(ctx, s)

	// Resolve spec.endpoint.stdio.env
	if s.Spec.Endpoint.Stdio != nil {
		p.secret.ProcessResource(ctx, mcpStdioEnvResource{s})
	}
}
