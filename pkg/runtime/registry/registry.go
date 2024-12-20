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
	"context"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components/bindings"
	"github.com/dapr/dapr/pkg/components/configuration"
	"github.com/dapr/dapr/pkg/components/conversation"
	"github.com/dapr/dapr/pkg/components/crypto"
	"github.com/dapr/dapr/pkg/components/lock"
	"github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/components/nameresolution"
	"github.com/dapr/dapr/pkg/components/pubsub"
	"github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/components/state"
	messagingv1 "github.com/dapr/dapr/pkg/messaging/v1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

type ComponentsCallback func(components ComponentRegistry) error

type ComponentRegistry struct {
	DirectMessaging messagingv1.DirectMessaging
	CompStore       *compstore.ComponentStore
}

type Reporter func(context.Context, componentsapi.Component, *operatorv1.ResourceResult) error

// Registry is a collection of component registries.
type Registry struct {
	secret         *secretstores.Registry
	state          *state.Registry
	config         *configuration.Registry
	lock           *lock.Registry
	pubsub         *pubsub.Registry
	nameResolution *nameresolution.Registry
	binding        *bindings.Registry
	httpMiddleware *http.Registry
	crypto         *crypto.Registry
	conversations  *conversation.Registry
	componentCb    ComponentsCallback
	reporter       Reporter
}

func New(opts *Options) *Registry {
	return &Registry{
		secret:         opts.secret,
		state:          opts.state,
		config:         opts.config,
		lock:           opts.lock,
		pubsub:         opts.pubsub,
		nameResolution: opts.nameResolution,
		binding:        opts.binding,
		httpMiddleware: opts.httpMiddleware,
		crypto:         opts.crypto,
		conversations:  opts.conversation,
		componentCb:    opts.componentsCallback,
		reporter:       opts.reporter,
	}
}

func (r *Registry) SecretStores() *secretstores.Registry {
	return r.secret
}

func (r *Registry) StateStores() *state.Registry {
	return r.state
}

func (r *Registry) Configurations() *configuration.Registry {
	return r.config
}

func (r *Registry) Locks() *lock.Registry {
	return r.lock
}

func (r *Registry) PubSubs() *pubsub.Registry {
	return r.pubsub
}

func (r *Registry) NameResolutions() *nameresolution.Registry {
	return r.nameResolution
}

func (r *Registry) Bindings() *bindings.Registry {
	return r.binding
}

func (r *Registry) HTTPMiddlewares() *http.Registry {
	return r.httpMiddleware
}

func (r *Registry) Crypto() *crypto.Registry {
	return r.crypto
}

func (r *Registry) Conversations() *conversation.Registry {
	return r.conversations
}

func (r *Registry) ComponentsCallback() ComponentsCallback {
	return r.componentCb
}

func (r *Registry) Reporter() Reporter {
	return r.reporter
}
