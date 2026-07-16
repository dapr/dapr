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
var DefaultRegistry *Registry = NewRegistry()

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

func (r *Registry) Create(name, version, logName string) (lock.Store, error) {
	if method, ok := r.getStore(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find lock store %s/%s", name, version)
}

func (r *Registry) getStore(name, version, logName string) (func() lock.Store, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	factoryMethod, ok := r.stores[nameLower+"/"+versionLower]
	if ok {
		return r.wrapFn(factoryMethod, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		factoryMethod, ok = r.stores[nameLower]
		if ok {
			return r.wrapFn(factoryMethod, logName), true
		}
	}
	return nil, false
}

func (r *Registry) wrapFn(componentFactory func(logger.Logger) lock.Store, logName string) func() lock.Store {
	return func() lock.Store {
		l := r.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(l)
	}
}

func createFullName(name string) string {
	return strings.ToLower("lock." + name)
}
