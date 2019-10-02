package bindings

import (
	"fmt"
	"sync"

	"github.com/dapr/components-contrib/bindings"
)

type BindingsRegistry interface {
	CreateInputBinding(name string) (bindings.InputBinding, error)
	CreateOutputBinding(name string) (bindings.OutputBinding, error)
}

type bindingsRegistry struct {
	inputBindings  map[string]bindings.InputBinding
	outputBindings map[string]bindings.OutputBinding
}

var instance *bindingsRegistry
var once sync.Once

func NewBindingsRegistry() BindingsRegistry {
	once.Do(func() {
		instance = &bindingsRegistry{
			inputBindings:  map[string]bindings.InputBinding{},
			outputBindings: map[string]bindings.OutputBinding{},
		}
	})
	return instance
}

func RegisterInputBinding(name string, binding bindings.InputBinding) {
	instance.inputBindings[fmt.Sprintf("bindings.%s", name)] = binding
}

func RegisterOutputBinding(name string, binding bindings.OutputBinding) {
	instance.outputBindings[fmt.Sprintf("bindings.%s", name)] = binding
}

func (b *bindingsRegistry) CreateInputBinding(name string) (bindings.InputBinding, error) {
	for key, binding := range b.inputBindings {
		if key == name {
			return binding, nil
		}
	}

	return nil, fmt.Errorf("couldn't find input binding %s", name)
}

func (b *bindingsRegistry) CreateOutputBinding(name string) (bindings.OutputBinding, error) {
	for key, binding := range b.outputBindings {
		if key == name {
			return binding, nil
		}
	}

	return nil, fmt.Errorf("couldn't find output binding %s", name)
}
