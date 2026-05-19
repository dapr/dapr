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

package reconciler

import (
	"context"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/processor"
)

type mcpservers struct {
	store *compstore.ComponentStore
	proc  *processor.Processor
	auth  *authorizer.Authorizer
	loader.Loader[mcpserverapi.MCPServer]
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (m *mcpservers) update(ctx context.Context, server mcpserverapi.MCPServer) {
	if !m.auth.IsObjectAuthorized(server) {
		log.Warnf("Received unauthorized MCPServer update, ignored: %s", server.LogName())
		return
	}

	// Close the existing server (compstore entry + workflow refcount) before
	// adding the new one. Without this, an update with a bad spec under
	// IgnoreErrors=true would leave the prior server in compstore and skip the
	// failing add silently; a valid update would re-call
	// wfengine.EnsureActorsRegistered, leaking per-name workflow registrations.
	if existing, ok := m.store.GetMCPServer(server.Name); ok {
		if differ.AreSame(existing, server) {
			log.Debugf("MCPServer update skipped: no changes detected: %s", server.LogName())
			return
		}
		log.Infof("Closing existing MCPServer to reload: %s", existing.LogName())
		m.proc.DeleteMCPServer(existing.Name)
	}

	log.Infof("MCPServer updated via hot-reload: %s", server.LogName())
	m.proc.AddPendingMCPServer(ctx, server)
}

//nolint:unused
func (m *mcpservers) delete(ctx context.Context, server mcpserverapi.MCPServer) {
	log.Infof("MCPServer deleted via hot-reload: %s", server.LogName())
	m.proc.DeleteMCPServer(server.Name)
}
