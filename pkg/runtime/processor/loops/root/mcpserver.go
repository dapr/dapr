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

package root

import (
	"github.com/dapr/kit/events/loop"

	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	mcploop "github.com/dapr/dapr/pkg/runtime/processor/loops/mcpserver"
)

func (r *Root) handleMCPServer(ev *loops.AddMCPServer) {
	// Bump in-flight before enqueueing so a Barrier received between the
	// enqueue and the per-name loop's MCPServerRegistered cannot close
	// prematurely.
	r.inFlight++
	r.mcpServerLoop(ev.Server.Name).Enqueue(ev)
}

func (r *Root) handleMCPServerRegistered() {
	r.decInFlight()
}

func (r *Root) handleDeleteMCPServer(ev *loops.DeleteMCPServer) {
	r.mcpServerLoop(ev.Name).Enqueue(ev)
}

func (r *Root) mcpServerLoop(name string) loop.Interface[loops.EventMCPServer] {
	if l, ok := r.mcpServers[name]; ok {
		return l
	}
	h := mcploop.New(mcploop.Options{
		Name:                name,
		CompStore:           r.compStore,
		Secret:              r.secret,
		KubernetesMode:      r.kubernetesMode,
		RegisterMCPServer:   r.registerMCPServer,
		UnregisterMCPServer: r.unregisterMCPServer,
		Root:                r.loop,
	})
	l := h.Loop()
	r.mcpServers[name] = l

	ctx := r.runCtx
	r.mcpServersWG.Go(func() {
		if err := l.Run(ctx); err != nil {
			log.Errorf("mcpserver loop %s error: %s", name, err)
		}
		loops.MCPServerFactory.CacheLoop(l)
	})
	return l
}
