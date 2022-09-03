package transaction

import (
	"strings"

	"github.com/dapr/components-contrib/transaction"
	"github.com/dapr/dapr/pkg/components"
	logger "github.com/dapr/kit/logger"
	"github.com/pkg/errors"
)

type Registry struct {
	Logger       logger.Logger
	transactions map[string]func(logger.Logger) transaction.Transaction
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry

func init() {
	DefaultRegistry = NewRegistry()
}

func NewRegistry() *Registry {
	return &Registry{
		transactions: make(map[string]func(logger.Logger) transaction.Transaction),
	}
}

func (r *Registry) RegisterComponent(componentFactory func(logger.Logger) transaction.Transaction, names ...string) {
	for _, name := range names {
		r.transactions[createFullName(name)] = componentFactory
	}
}

func (r *Registry) Create(name, version string) (transaction.Transaction, error) {
	if method, ok := r.getTransaction(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find transaction %s/%s", name, version)
}

func (r *Registry) getTransaction(name, version string) (func() transaction.Transaction, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)

	transactionFn, ok := r.transactions[nameLower+"/"+versionLower]
	if ok {
		return r.wrapFn(transactionFn), true
	}

	if components.IsInitialVersion(versionLower) {
		transactionFn, ok = r.transactions[nameLower]
		if ok {
			return r.wrapFn(transactionFn), true
		}
	}
	return nil, false
}

func (r *Registry) wrapFn(componentFactory func(logger.Logger) transaction.Transaction) func() transaction.Transaction {
	return func() transaction.Transaction {
		return componentFactory(r.Logger)
	}
}

func createFullName(name string) string {
	return strings.ToLower("transaction." + name)
}
