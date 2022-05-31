package transaction

import (
	"github.com/dapr/components-contrib/transaction"
	"github.com/dapr/dapr/pkg/components"
	"github.com/pkg/errors"
	"strings"
	_ "strings"

	_ "github.com/pkg/errors"

	_ "github.com/dapr/components-contrib/transaction"
	_ "github.com/dapr/dapr/pkg/components"
)

type Transaction struct {
	Name          string
	FactoryMethod func() transaction.Transaction
}

func New(name string, factoryMethod func() transaction.Transaction) Transaction {
	return Transaction{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

type Registry interface {
	Register(components ...Transaction)
	Create(name, version string) (transaction.Transaction, error)
}

type transactionRegistry struct {
	transactions map[string]func() transaction.Transaction
}

func NewRegistry() Registry {
	return &transactionRegistry{
		transactions: map[string]func() transaction.Transaction{},
	}
}

func (t *transactionRegistry) Register(components ...Transaction) {
	for _, component := range components {
		t.transactions[createFullName(component.Name)] = component.FactoryMethod
	}
}

func (t *transactionRegistry) Create(name, version string) (transaction.Transaction, error) {
	if method, ok := t.getTransaction(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find transaction %s/%s", name, version)
}

func (t *transactionRegistry) getTransaction(name, version string) (func() transaction.Transaction, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	transactionFn, ok := t.transactions[nameLower+"/"+versionLower]
	if ok {
		return transactionFn, true
	}
	if components.IsInitialVersion(versionLower) {
		transactionFn, ok = t.transactions[nameLower]
	}
	return transactionFn, ok
}

func createFullName(name string) string {
	return strings.ToLower("state." + name)
}