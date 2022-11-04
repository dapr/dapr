package runtime

import (
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
)

type (
	// runtimeOpts encapsulates the components to include in the runtime.
	runtimeOpts struct {
		secretStoreRegistry    *secretstoresLoader.Registry
		stateRegistry          *stateLoader.Registry
		configurationRegistry  *configurationLoader.Registry
		lockRegistry           *lockLoader.Registry
		pubsubRegistry         *pubsubLoader.Registry
		nameResolutionRegistry *nrLoader.Registry
		bindingRegistry        *bindingsLoader.Registry
		httpMiddlewareRegistry *httpMiddlewareLoader.Registry
		componentsCallback     ComponentsCallback
	}

	// Option is a function that customizes the runtime.
	Option func(o *runtimeOpts)
)

// WithSecretStores adds secret store components to the runtime.
func WithSecretStores(registry *secretstoresLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.secretStoreRegistry = registry
	}
}

// WithStates adds state store components to the runtime.
func WithStates(registry *stateLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.stateRegistry = registry
	}
}

// WithConfigurations adds configuration store components to the runtime.
func WithConfigurations(registry *configurationLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.configurationRegistry = registry
	}
}

// WithLocks add lock store components to the runtime.
func WithLocks(registry *lockLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.lockRegistry = registry
	}
}

// WithPubSubs adds pubsub store components to the runtime.
func WithPubSubs(registry *pubsubLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.pubsubRegistry = registry
	}
}

// WithNameResolutions adds name resolution components to the runtime.
func WithNameResolutions(registry *nrLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.nameResolutionRegistry = registry
	}
}

// WithBindings adds binding components to the runtime.
func WithBindings(registry *bindingsLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.bindingRegistry = registry
	}
}

// WithHTTPMiddlewares adds HTTP middleware components to the runtime.
func WithHTTPMiddlewares(registry *httpMiddlewareLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.httpMiddlewareRegistry = registry
	}
}

// WithComponentsCallback sets the components callback for applications that embed Dapr.
func WithComponentsCallback(componentsCallback ComponentsCallback) Option {
	return func(o *runtimeOpts) {
		o.componentsCallback = componentsCallback
	}
}
