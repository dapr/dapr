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
	wrap  middleware.HTTP
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
		wrap:  func(next http.Handler) http.Handler { return next },
	}
}

// http returns a dynamic HTTP middleware. The chained handler will dynamically
// instrument the HTTP middlewares as they are added, updated and removed from
// the store. Consumers must call `buildChain` for the handler changes to take
// effect.
// The pipeline root handler will be set once the middleware is invoked.
func (p *pipeline) http() middleware.HTTP {
	p.buildChain()

	return func(root http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.lock.RLock()
			wrap := p.wrap
			p.lock.RUnlock()
			wrap(root).ServeHTTP(w, r)
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
		p.wrap = func(next http.Handler) http.Handler { return next }
		return
	}

	log.Infof("Building pipeline %s", p.name)

	var handlers []middleware.HTTP
	for _, spec := range p.spec.Handlers {
		handler, ok := p.store.Get(store.Metadata{
			Name:    spec.Name,
			Type:    spec.Type,
			Version: spec.Version,
		})
		if !ok {
			continue
		}
		handlers = append(handlers, handler)
	}

	p.wrap = func(next http.Handler) http.Handler {
		for i := len(handlers) - 1; i >= 0; i-- {
			next = handlers[i](next)
		}
		return next
	}
}
