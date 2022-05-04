package lock

import (
	"strings"

	"github.com/pkg/errors"

	"github.com/dapr/components-contrib/lock"
	"github.com/dapr/dapr/pkg/components"
)

type Lock struct {
	Name          string
	FactoryMethod func() lock.Store
}

func New(name string, f func() lock.Store) Lock {
	return Lock{
		Name:          name,
		FactoryMethod: f,
	}
}

type Registry interface {
	Register(fs ...Lock)
	Create(name, version string) (lock.Store, error)
}

type lockRegistry struct {
	stores map[string]func() lock.Store
}

func NewRegistry() Registry {
	return &lockRegistry{
		stores: make(map[string]func() lock.Store),
	}
}

// Register registers a new factory method that creates an instance of a lock.Store.
// The key is the name of the state store, eg. redis.
func (r *lockRegistry) Register(fs ...Lock) {
	for _, f := range fs {
		r.stores[createFullName(f.Name)] = f.FactoryMethod
	}
}

func (r *lockRegistry) Create(name, version string) (lock.Store, error) {
	if method, ok := r.getStore(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find lock store %s/%s", name, version)
}

func (r *lockRegistry) getStore(name, version string) (func() lock.Store, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	factoryMethod, ok := r.stores[nameLower+"/"+versionLower]
	if ok {
		return factoryMethod, true
	}
	if components.IsInitialVersion(versionLower) {
		factoryMethod, ok = r.stores[nameLower]
	}
	return factoryMethod, ok
}

func createFullName(name string) string {
	return strings.ToLower("lock." + name)
}
