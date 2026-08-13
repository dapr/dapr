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
		kind:     mcpserverapi.Kind,
		htarget:  opts.Healthz.AddTarget("mcpserver-reconciler"),
		interval: opts.ReconcileInterval,
		clock:    clock.RealClock{},
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
func (m *mcpservers) update(ctx context.Context, server mcpserverapi.MCPServer) error {
	if !m.auth.IsObjectAuthorized(server) {
		log.Warn("Received unauthorized MCPServer update, ignored", "log_name", server.LogName())
		return nil
	}

	if existing, ok := m.store.GetMCPServer(server.Name); ok {
		// Resolve the incoming spec's secretKeyRef/envRef values before
		// comparing. The stored copy was resolved when it was loaded
		// (ProcessMCPServerSecrets), so comparing it against the raw incoming
		// spec would always differ for a secret-ref header and reload on every
		// reconcile tick. Comparing resolved-vs-resolved skips a genuine no-op
		// while still reloading on a real secret rotation (same secretKeyRef, new
		// value). Mirrors the Component reconciler (see components.go).
		//
		// Resolve a copy, not server itself: the raw spec must reach
		// AddPendingMCPServer below so the processor resolves it exactly once on
		// reload (resolving in place would have the processor decode an
		// already-decoded value).
		resolved := server.DeepCopy()
		m.proc.ProcessMCPServerSecrets(ctx, resolved)
		if differ.AreSame(existing, *resolved) {
			log.Debug("MCPServer update skipped: no changes detected: " + server.LogName())
			return nil
		}

		// See components.update for the rationale on this guard.
		if server.GetGeneration() > 0 && server.GetGeneration() < existing.GetGeneration() {
			log.Warn("Ignoring stale MCPServer event",
				"server", server.LogName(), "generation", server.GetGeneration(), "installed_generation", existing.GetGeneration())
			return nil
		}

		// Close the existing server (compstore entry + workflow refcount) before
		// adding the new one. Without this, every update would re-call
		// wfengine.EnsureActorsRegistered (bumping the refcount) and leak
		// per-name workflow registrations.
		log.Info("Closing existing MCPServer to reload: " + existing.LogName())
		m.proc.DeleteMCPServer(ctx, existing.Name)
	}

	log.Info("Adding MCPServer for processing", "log_name", server.LogName())

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
			log.Error("Ignoring error processing MCPServer", "error", err)
			return nil
		}
		log.Warn("Error processing MCPServer, daprd will exit gracefully", "error", err)
		return err
	}
}

// The go linter does not understand that delete is used by the generic
// reconciler via the manager interface.
//
//nolint:unused
func (m *mcpservers) delete(ctx context.Context, server mcpserverapi.MCPServer) error {
	log.Info("MCPServer deleted via hot-reload", "log_name", server.LogName())
	m.proc.DeleteMCPServer(ctx, server.Name)
	return nil
}
