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

package pluggable

import (
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/bindings"
	"github.com/dapr/dapr/pkg/components/configuration"
	"github.com/dapr/dapr/pkg/components/lock"
	"github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/components/nameresolution"
	"github.com/dapr/dapr/pkg/components/pubsub"
	"github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/components/state"
)

type registryOpts struct {
	registries map[components.PluggableType]func(components.Pluggable)
}

type Option = func(*registryOpts)

// withRegistry adds a new register function for the puggable registry.
func withRegistry[T any](cmpType components.PluggableType, regFunc func(...T), factory func(components.Pluggable) T) Option {
	return func(ro *registryOpts) {
		ro.registries[cmpType] = func(pc components.Pluggable) {
			regFunc(factory(pc))
		}
	}
}

// WithBindingsRegistry adds a register function for bindings.
func WithBindingsRegistry(reg bindings.Registry) Option {
	return func(ro *registryOpts) {
		withInput := withRegistry(components.InputBinding, reg.RegisterInputBindings, bindings.NewInputFromPluggable)
		withInput(ro)
		withOutput := withRegistry(components.OutputBinding, reg.RegisterOutputBindings, bindings.NewOutputFromPluggable)
		withOutput(ro)
	}
}

// WithConfigurationRegistry adds a register function for configurations.
func WithConfigurationRegistry(reg configuration.Registry) Option {
	return withRegistry(components.Configuration, reg.Register, configuration.NewFromPluggable)
}

// WithLockRegistry adds a register function for locks.
func WithLockRegistry(reg lock.Registry) Option {
	return withRegistry(components.Lock, reg.Register, lock.NewFromPluggable)
}

// WithHTTPMiddlewareRegistry adds a register function for httpmiddlewares.
func WithHTTPMiddlewareRegistry(reg http.Registry) Option {
	return withRegistry(components.HTTPMiddleware, reg.Register, http.NewFromPluggable)
}

// WithNameResolutionRegistry adds a register function for name resolutions.
func WithNameResolutionRegistry(reg nameresolution.Registry) Option {
	return withRegistry(components.NameResolution, reg.Register, nameresolution.NewFromPluggable)
}

// WithPubSubRegistry adds a register function for pubsub.
func WithPubSubRegistry(reg pubsub.Registry) Option {
	return withRegistry(components.PubSub, reg.Register, pubsub.NewFromPluggable)
}

// WithSecretStoresRegistry adds a register function for secret stores.
func WithSecretStoresRegistry(reg secretstores.Registry) Option {
	return withRegistry(components.Secret, reg.Register, secretstores.NewFromPluggable)
}

// WithStateStoreRegistry adds a register function for state stores.
func WithStateStoreRegistry(reg state.Registry) Option {
	return withRegistry(components.State, reg.Register, state.NewFromPluggable)
}
