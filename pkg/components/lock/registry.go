package lock

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/lock"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

type Registry struct {
	Logger logger.Logger
	stores map[string]func(logger.Logger) lock.Store
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry

func init() {
	DefaultRegistry = NewRegistry()
}

func NewRegistry() *Registry {
	return &Registry{
		stores: make(map[string]func(logger.Logger) lock.Store),
	}
}

func (r *Registry) RegisterComponent(componentFactory func(logger.Logger) lock.Store, names ...string) {
	for _, name := range names {
		r.stores[createFullName(name)] = componentFactory
	}
}

func (r *Registry) Create(name, version string) (lock.Store, error) {
	if method, ok := r.getStore(name, version); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find lock store %s/%s", name, version)
}

func (r *Registry) getStore(name, version string) (func() lock.Store, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	factoryMethod, ok := r.stores[nameLower+"/"+versionLower]
	if ok {
		return r.wrapFn(factoryMethod), true
	}
	if components.IsInitialVersion(versionLower) {
		factoryMethod, ok = r.stores[nameLower]
		if ok {
			return r.wrapFn(factoryMethod), true
		}
	}
	return nil, false
}

func (r *Registry) wrapFn(componentFactory func(logger.Logger) lock.Store) func() lock.Store {
	return func() lock.Store {
		return componentFactory(r.Logger)
	}
}

func createFullName(name string) string {
	return strings.ToLower("lock." + name)
}
