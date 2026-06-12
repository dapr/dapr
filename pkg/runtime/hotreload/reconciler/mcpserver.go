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
	"fmt"

	"k8s.io/utils/clock"

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

func NewMCPServers(opts Options[mcpserverapi.MCPServer]) *Reconciler[mcpserverapi.MCPServer] {
	r := &Reconciler[mcpserverapi.MCPServer]{
		kind:    mcpserverapi.Kind,
		htarget: opts.Healthz.AddTarget("mcpserver-reconciler"),
		clock:   clock.RealClock{},
		manager: &mcpservers{
			Loader: opts.Loader.MCPServers(),
			store:  opts.CompStore,
			proc:   opts.Processor,
			auth:   opts.Authorizer,
		},
	}
	r.loop = loopFactory.NewLoop(r)
	return r
}

// The go linter does not yet understand that these functions are being used by
// the generic reconciler.
//
//nolint:unused
func (m *mcpservers) update(ctx context.Context, server mcpserverapi.MCPServer) error {
	if !m.auth.IsObjectAuthorized(server) {
		log.Warnf("Received unauthorized MCPServer update, ignored: %s", server.LogName())
		return nil
	}

	existing, exists := m.store.GetMCPServer(server.Name)
	if exists {
		if differ.AreSame(existing, server) {
			log.Debugf("MCPServer update skipped: no changes detected: %s", server.LogName())
			return nil
		}

		// See components.update for the rationale on this guard.
		if server.GetGeneration() > 0 && server.GetGeneration() < existing.GetGeneration() {
			log.Warnf("Ignoring stale MCPServer event for %s (generation %d < installed %d)",
				server.LogName(), server.GetGeneration(), existing.GetGeneration())
			return nil
		}

		// Close the existing server (compstore entry + workflow refcount) before
		// adding the new one. Without this, every update would re-call
		// wfengine.EnsureActorsRegistered (bumping the refcount) and leak
		// per-name workflow registrations.
		log.Infof("Closing existing MCPServer to reload: %s", existing.LogName())
		m.proc.DeleteMCPServer(ctx, existing.Name)
	}

	log.Infof("Adding MCPServer for processing: %s", server.LogName())

	res := m.proc.AddPendingMCPServer(ctx, server)
	if res == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-res:
		if err == nil {
			return nil
		}
		err = fmt.Errorf("process MCPServer %s error: %s", server.Name, err)
		if server.Spec.IgnoreErrors {
			log.Errorf("Ignoring error processing MCPServer: %s", err)
			return nil
		}
		log.Warnf("Error processing MCPServer, daprd will exit gracefully: %s", err)
		return err
	}
}

//nolint:unused
func (m *mcpservers) delete(ctx context.Context, server mcpserverapi.MCPServer) error {
	log.Infof("MCPServer deleted via hot-reload: %s", server.LogName())
	m.proc.DeleteMCPServer(ctx, server.Name)
	return nil
}
