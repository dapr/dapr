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

package mcpserver

import (
	"context"
	"fmt"

	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/internal/loader/validate"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

var log = logger.NewLogger("dapr.runtime.processor.loops.mcpserver")

// SecretProcessor resolves secret references on a resource. Same shape as the
// one used by the root loop; passed through so per-name loops don't pull in
// the secret package directly.
type SecretProcessor interface {
	ProcessResource(ctx context.Context, r meta.Resource) (bool, string)
}

// Options configures a per-name MCPServer loop.
type Options struct {
	Name                string
	CompStore           *compstore.ComponentStore
	Secret              SecretProcessor
	KubernetesMode      bool
	RegisterMCPServer   func(ctx context.Context, s mcpserverapi.MCPServer) error
	UnregisterMCPServer func(name string)
	// Root is the parent loop. The per-name loop posts MCPServerRegistered
	// back into it so root can update its in-flight counter.
	Root loop.Interface[loops.EventRoot]
}

// Handler is the loop.Handler[loops.EventMCPServer] for a single MCPServer
// name. All events for one name are dispatched to one Handler so Add/Delete
// pairs serialise in submission order even when distinct names run in
// parallel on their own loops.
type Handler struct {
	name                string
	compStore           *compstore.ComponentStore
	secret              SecretProcessor
	kubernetesMode      bool
	registerMCPServer   func(ctx context.Context, s mcpserverapi.MCPServer) error
	unregisterMCPServer func(name string)
	root                loop.Interface[loops.EventRoot]

	loop loop.Interface[loops.EventMCPServer]
}

// New constructs a per-name MCPServer loop handler. The caller must invoke
// Run on the returned Handler's Loop().
func New(opts Options) *Handler {
	h := &Handler{
		name:                opts.Name,
		compStore:           opts.CompStore,
		secret:              opts.Secret,
		kubernetesMode:      opts.KubernetesMode,
		registerMCPServer:   opts.RegisterMCPServer,
		unregisterMCPServer: opts.UnregisterMCPServer,
		root:                opts.Root,
	}
	h.loop = loops.MCPServerFactory.NewLoop(h)
	return h
}

// Loop returns the underlying per-name loop.
func (h *Handler) Loop() loop.Interface[loops.EventMCPServer] { return h.loop }

// Handle dispatches one event for this MCPServer name.
func (h *Handler) Handle(ctx context.Context, e loops.EventMCPServer) error {
	switch ev := e.(type) {
	case *loops.AddMCPServer:
		h.handleAdd(ctx, ev)
	case *loops.DeleteMCPServer:
		h.handleDelete(ev)
	case *loops.Shutdown:
		// No per-name shutdown work: holder teardown happens in the
		// inprocess executor's closeAll. Nothing else owns state here.
	}
	return nil
}

func (h *Handler) handleAdd(ctx context.Context, ev *loops.AddMCPServer) {
	defer h.root.Enqueue(&loops.MCPServerRegistered{})

	s := ev.Server
	if s.Name == "" {
		sendResult(ev.Result, nil)
		return
	}

	if err := validate.MCPServer(ctx, &s); err != nil {
		sendResult(ev.Result, fmt.Errorf("MCPServer %q failed validation: %w", s.Name, err))
		return
	}
	if err := validate.MCPServerSecurity(&s, h.kubernetesMode); err != nil {
		sendResult(ev.Result, fmt.Errorf("MCPServer %q failed security validation: %w", s.Name, err))
		return
	}

	h.secret.ProcessResource(ctx, &s)
	if s.Spec.Endpoint.Stdio != nil {
		h.secret.ProcessResource(ctx, mcpStdioEnvResource{MCPServer: &s})
	}

	// Commit to compstore before workflow registration. Matches the
	// pre-refactor behaviour: under IgnoreErrors=true, an MCPServer whose
	// workflow registration fails is still considered "loaded" and
	// observable via the metadata API.
	h.compStore.AddMCPServer(s)
	log.Infof("MCPServer loaded: %s", s.LogName())

	if h.registerMCPServer == nil {
		sendResult(ev.Result, nil)
		return
	}
	if err := h.registerMCPServer(ctx, s); err != nil {
		sendResult(ev.Result, fmt.Errorf("MCPServer %q: failed to register workflows: %w", s.Name, err))
		return
	}
	sendResult(ev.Result, nil)
}

func (h *Handler) handleDelete(ev *loops.DeleteMCPServer) {
	h.compStore.DeleteMCPServer(ev.Name)
	if h.unregisterMCPServer != nil {
		h.unregisterMCPServer(ev.Name)
	}
	if ev.Done != nil {
		close(ev.Done)
	}
}

// mcpStdioEnvResource is a thin adapter that wraps an MCPServer and overrides
// NameValuePairs to return Spec.Endpoint.Stdio.Env instead of the HTTP
// transport headers.
type mcpStdioEnvResource struct {
	*mcpserverapi.MCPServer
}

func (r mcpStdioEnvResource) NameValuePairs() []commonapi.NameValuePair {
	if r.Spec.Endpoint.Stdio == nil {
		return nil
	}
	return r.Spec.Endpoint.Stdio.Env
}

func sendResult(ch chan<- error, err error) {
	if ch == nil {
		return
	}
	select {
	case ch <- err:
	default:
	}
}
