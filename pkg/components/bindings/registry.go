// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/bindings"
)

// Registry is the interface of a components that allows callers to get registered instances of input and output bindings
type Registry interface {
	CreateInputBinding(name string) (bindings.InputBinding, error)
	CreateOutputBinding(name string) (bindings.OutputBinding, error)
}

type bindingsRegistry struct {
	inputBindings  map[string]func() bindings.InputBinding
	outputBindings map[string]func() bindings.OutputBinding
}

var instance *bindingsRegistry
var once sync.Once

// NewRegistry is used to create new bindings
func NewRegistry() Registry {
	once.Do(func() {
		instance = &bindingsRegistry{
			inputBindings:  map[string]func() bindings.InputBinding{},
			outputBindings: map[string]func() bindings.OutputBinding{},
		}
	})
	return instance
}

// RegisterInputBinding registers a new factory method that creates an instance of an InputBinding.
// The key is the name of the binding, eg. kafka.
func RegisterInputBinding(name string, factoryMethod func() bindings.InputBinding) {
	instance.inputBindings[fmt.Sprintf("bindings.%s", name)] = factoryMethod
}

// RegisterOutputBinding registers a new factory method that creates an instance of an OutputBinding.
// The key is the name of the binding, eg. kafka.
func RegisterOutputBinding(name string, factoryMethod func() bindings.OutputBinding) {
	instance.outputBindings[fmt.Sprintf("bindings.%s", name)] = factoryMethod
}

func (b *bindingsRegistry) CreateInputBinding(name string) (bindings.InputBinding, error) {
	for key, method := range b.inputBindings {
		if key == name {
			return method(), nil
		}
	}
	return nil, fmt.Errorf("couldn't find input binding %s", name)
}

func (b *bindingsRegistry) CreateOutputBinding(name string) (bindings.OutputBinding, error) {
	for key, method := range b.outputBindings {
		if key == name {
			return method(), nil
		}
	}
	return nil, fmt.Errorf("couldn't find output binding %s", name)
}
