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
	httploop "github.com/dapr/dapr/pkg/runtime/processor/loops/httpendpoint"
)

func (r *Root) handleHTTPEndpoint(ev *loops.AddHTTPEndpoint) {
	// Bump in-flight before enqueueing so a Barrier received between the
	// enqueue and the per-name loop's HTTPEndpointAdded cannot close
	// prematurely.
	r.inFlight++
	r.httpEndpointLoop(ev.Endpoint.Name).Enqueue(ev)
}

func (r *Root) handleHTTPEndpointAdded() {
	if r.inFlight > 0 {
		r.inFlight--
	}
	if r.inFlight == 0 {
		for _, done := range r.pendingBarriers {
			close(done)
		}
		r.pendingBarriers = nil
	}
}

// httpEndpointLoop returns (lazily creating) the per-name HTTPEndpoint loop.
// Loops outlive individual Add events: keeping them around preserves FIFO
// ordering across delete + re-add for the same name.
func (r *Root) httpEndpointLoop(name string) loop.Interface[loops.EventHTTPEndpoint] {
	if l, ok := r.httpEndpoints[name]; ok {
		return l
	}
	h := httploop.New(httploop.Options{
		Name:      name,
		CompStore: r.compStore,
		Secret:    r.secret,
		Root:      r.loop,
	})
	l := h.Loop()
	r.httpEndpoints[name] = l

	ctx := r.runCtx
	r.httpEndpointsWG.Go(func() {
		if err := l.Run(ctx); err != nil {
			log.Errorf("httpendpoint loop %s error: %s", name, err)
		}
		loops.HTTPEndpointFactory.CacheLoop(l)
	})
	return l
}
