/*
Copyright 2024 The Dapr Authors
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
	"sync"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/dapr/pkg/middleware/store"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.middleware.http")

// HTTP returns HTTP middleware pipelines. These pipelines dynamically update
// the middleware chain when a component is added or removed from the store.
// Callers need only build a Pipeline once for a given spec.
type HTTP struct {
	lock      sync.RWMutex
	store     *store.Store[middleware.HTTP]
	pipelines []*pipeline
}

// Spec is a specification for a creating a middleware.
type Spec struct {
	Component      compapi.Component
	Implementation middleware.HTTP
}

// New returns a new HTTP middleware store.
func New() *HTTP {
	return &HTTP{
		store: store.New[middleware.HTTP]("http"),
	}
}

// Add adds a middleware to the store.
func (h *HTTP) Add(spec Spec) {
	h.store.Add(store.Item[middleware.HTTP]{
		Metadata: store.Metadata{
			Name:    spec.Component.Name,
			Type:    spec.Component.Spec.Type,
			Version: spec.Component.Spec.Version,
		},
		Middleware: spec.Implementation,
	})
	for _, p := range h.pipelines {
		p.buildChain()
	}
}

// Remove removes a middleware from the store.
func (h *HTTP) Remove(name string) {
	h.store.Remove(name)
	for _, p := range h.pipelines {
		p.buildChain()
	}
}

// BuildPipelineFromSpec builds a middleware pipeline from a spec. The returned
// Pipeline will dynamically update handlers when middleware components are
// added or removed from the store.
func (h *HTTP) BuildPipelineFromSpec(name string, spec *config.PipelineSpec) middleware.HTTP {
	h.lock.Lock()
	defer h.lock.Unlock()

	p := newPipeline(name, h.store, spec)
	h.pipelines = append(h.pipelines, p)
	return p.http()
}
