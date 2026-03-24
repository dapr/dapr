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
)

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

		p.processMCPServerSecrets(ctx, &s)
		p.compStore.AddMCPServer(s)
		log.Infof("MCPServer loaded: %s", s.LogName())
	}

	return nil
}

// processMCPServerSecrets resolves secretKeyRef and envRef entries in spec.headers
// and stdio.env using the configured secret store.
func (p *Processor) processMCPServerSecrets(ctx context.Context, s *mcpserverapi.MCPServer) {
	// p.secret.ProcessResource resolves all NameValuePair entries (secretKeyRef + envRef)
	// in the pairs returned by s.NameValuePairs() — which covers spec.headers.
	_, _ = p.secret.ProcessResource(ctx, s)
}
