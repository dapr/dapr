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

// withOpts applies all given options to runtime.
func withOpts(opts ...Option) Option {
	return func(runtimeOpts *runtimeOpts) {
		for _, opt := range opts {
			opt(runtimeOpts)
		}
	}
}

// pluggableOptions maps a component type to its pluggable component loader.
var pluggableOptions = make(map[components.Type]func(pluggable.Component) Option)

func init() {
	useOption(components.State, WithStates)
	useOption(components.PubSub, WithPubSubs)
	useOption(components.InputBinding, WithInputBindings)
	useOption(components.OutputBinding, WithOutputBindings)
	useOption(components.HTTPMiddleware, WithHTTPMiddleware)
	useOption(components.Configuration, WithConfigurations)
	useOption(components.Secret, WithSecretStores)
	useOption(components.Lock, WithLocks)
	useOption(components.NameResolution, WithNameResolutions)
}

// useOption adds (or replace) a new pluggable loader to the loader map.
func useOption[T any](componentType components.Type, add func(...T) Option) {
	pluggableOptions[componentType] = func(pc pluggable.Component) Option {
		return add(pluggable.MustLoad[T](pc))
	}
}

// WithPluggables parses and adds a new component into the target component list.
func WithPluggables(pluggables ...pluggable.Component) Option {
	opts := make([]Option, 0)
	for _, pluggable := range pluggables {
		load, ok := pluggableOptions[components.Type(pluggable.Type)]
		// ignoring unknown components
		if ok {
			opts = append(opts, load(pluggable))
		}
	}
	return withOpts(opts...)
}
