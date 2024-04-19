/*
Copyright 2023 The Dapr Authors
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

package http

import (
	"net/http"
	"sync"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/dapr/pkg/middleware/store"
)

// pipeline manages a single HTTP middleware pipeline for a single HTTP
// Pipeline Spec.
type pipeline struct {
	lock  sync.RWMutex
	name  string
	spec  *config.PipelineSpec
	store *store.Store[middleware.HTTP]
	root  http.Handler
	chain http.Handler
}

// newPipeline creates a new HTTP Middleware Pipeline.
func newPipeline(
	name string,
	store *store.Store[middleware.HTTP],
	spec *config.PipelineSpec,
) *pipeline {
	return &pipeline{
		name:  name,
		spec:  spec,
		store: store,
	}
}

// http returns a dynamic HTTP middleware. The chained handler will dynamically
// instrument the HTTP middlewares as they are added, updated and removed from
// the store. Consumers must call `buildChain` for the handler changes to take
// effect.
// The pipeline root handler will be set once the middleware is invoked.
func (p *pipeline) http() middleware.HTTP {
	return func(root http.Handler) http.Handler {
		p.lock.Lock()
		p.root = root
		p.lock.Unlock()
		p.buildChain()

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.lock.RLock()
			next := p.chain
			p.lock.RUnlock()
			next.ServeHTTP(w, r)
		})
	}
}

// buildChain builds and updates the middleware chain from root using the set
// spec. Any middlewares which are not currently loaded are skipped.
func (p *pipeline) buildChain() {
	p.lock.Lock()
	defer p.lock.Unlock()

	// If no spec or no handlers defined, use root.
	if p.spec == nil || len(p.spec.Handlers) == 0 {
		p.chain = p.root
		return
	}

	log.Infof("Building pipeline %s", p.name)

	next := p.root
	for i := len(p.spec.Handlers) - 1; i >= 0; i-- {
		handler, ok := p.store.Get(store.Metadata{
			Name:    p.spec.Handlers[i].Name,
			Type:    p.spec.Handlers[i].Type,
			Version: p.spec.Handlers[i].Version,
		})
		if !ok {
			continue
		}
		next = handler(next)
	}

	p.chain = next
}
