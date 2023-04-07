package runtime

import (
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	cryptoLoader "github.com/dapr/dapr/pkg/components/crypto"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	workflowsLoader "github.com/dapr/dapr/pkg/components/workflows"
)

type (
	// runtimeOpts encapsulates the components to include in the runtime.
	runtimeOpts struct {
		secretStoreRegistry       *secretstoresLoader.Registry
		stateRegistry             *stateLoader.Registry
		configurationRegistry     *configurationLoader.Registry
		lockRegistry              *lockLoader.Registry
		pubsubRegistry            *pubsubLoader.Registry
		nameResolutionRegistry    *nrLoader.Registry
		bindingRegistry           *bindingsLoader.Registry
		httpMiddlewareRegistry    *httpMiddlewareLoader.Registry
		workflowComponentRegistry *workflowsLoader.Registry
		cryptoProviderRegistry    *cryptoLoader.Registry
		componentsCallback        ComponentsCallback
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

// WithCryptoProviders adds crypto provider components to the runtime.
func WithCryptoProviders(registry *cryptoLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.cryptoProviderRegistry = registry
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

// WithWorkflowComponents adds workflow components to the runtime.
func WithWorkflowComponents(registry *workflowsLoader.Registry) Option {
	return func(o *runtimeOpts) {
		o.workflowComponentRegistry = registry
	}
}

// WithComponentsCallback sets the components callback for applications that embed Dapr.
func WithComponentsCallback(componentsCallback ComponentsCallback) Option {
	return func(o *runtimeOpts) {
		o.componentsCallback = componentsCallback
	}
}
