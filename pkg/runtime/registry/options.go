/*
Copyright 2021 The Dapr Authors
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

package registry

import (
	"github.com/dapr/dapr/pkg/components/bindings"
	"github.com/dapr/dapr/pkg/components/configuration"
	"github.com/dapr/dapr/pkg/components/crypto"
	"github.com/dapr/dapr/pkg/components/lock"
	"github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/components/nameresolution"
	"github.com/dapr/dapr/pkg/components/pubsub"
	"github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/components/state"
	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
	"github.com/dapr/dapr/pkg/components/workflows"
)

// Options is the options to configure the registries
type Options struct {
	secret             *secretstores.Registry
	state              *state.Registry
	config             *configuration.Registry
	lock               *lock.Registry
	pubsub             *pubsub.Registry
	nameResolution     *nameresolution.Registry
	binding            *bindings.Registry
	httpMiddleware     *http.Registry
	workflow           *workflows.Registry
	workflowBackend    *wfbe.Registry
	crypto             *crypto.Registry
	componentsCallback ComponentsCallback
}

func NewOptions() *Options {
	return &Options{
		secret:         secretstores.DefaultRegistry,
		state:          state.DefaultRegistry,
		config:         configuration.DefaultRegistry,
		lock:           lock.DefaultRegistry,
		pubsub:         pubsub.DefaultRegistry,
		nameResolution: nameresolution.DefaultRegistry,
		binding:        bindings.DefaultRegistry,
		httpMiddleware: http.DefaultRegistry,
		crypto:         crypto.DefaultRegistry,
	}
}

// WithSecretStores adds secret store components to the runtime.
func (o *Options) WithSecretStores(registry *secretstores.Registry) *Options {
	o.secret = registry
	return o
}

// WithStateStores adds state store components to the runtime.
func (o *Options) WithStateStores(registry *state.Registry) *Options {
	o.state = registry
	return o
}

// WithConfigurations adds configuration store components to the runtime.
func (o *Options) WithConfigurations(registry *configuration.Registry) *Options {
	o.config = registry
	return o
}

// WithLocks adds lock store components to the runtime.
func (o *Options) WithLocks(registry *lock.Registry) *Options {
	o.lock = registry
	return o
}

// WithPubSubs adds pubsub components to the runtime.
func (o *Options) WithPubSubs(registry *pubsub.Registry) *Options {
	o.pubsub = registry
	return o
}

// WithNameResolution adds name resolution components to the runtime.
func (o *Options) WithNameResolutions(registry *nameresolution.Registry) *Options {
	o.nameResolution = registry
	return o
}

// WithBindings adds binding components to the runtime.
func (o *Options) WithBindings(registry *bindings.Registry) *Options {
	o.binding = registry
	return o
}

// WithHTTPMiddlewares adds http middleware components to the runtime.
func (o *Options) WithHTTPMiddlewares(registry *http.Registry) *Options {
	o.httpMiddleware = registry
	return o
}

// WithWorkflows adds workflow components to the runtime.
func (o *Options) WithWorkflows(registry *workflows.Registry) *Options {
	o.workflow = registry
	return o
}

// WithWorkflows adds workflow components to the runtime.
func (o *Options) WithWorkflowBackends(registry *wfbe.Registry) *Options {
	o.workflowBackend = registry
	return o
}

// WithCryptoProviders adds crypto components to the runtime.
func (o *Options) WithCryptoProviders(registry *crypto.Registry) *Options {
	o.crypto = registry
	return o
}

// WithComponentsCallback sets the components callback for applications that embed Dapr.
func (o *Options) WithComponentsCallback(componentsCallback ComponentsCallback) *Options {
	o.componentsCallback = componentsCallback
	return o
}
