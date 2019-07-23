package bindings

import (
	"fmt"
	"sync"
)

type BindingsRegistry interface {
	CreateInputBinding(name string) (InputBinding, error)
	CreateOutputBinding(name string) (OutputBinding, error)
}

type bindingsRegistry struct {
	inputBindings  map[string]InputBinding
	outputBindings map[string]OutputBinding
}

var instance *bindingsRegistry
var once sync.Once

func NewBindingsRegistry() BindingsRegistry {
	once.Do(func() {
		instance = &bindingsRegistry{
			inputBindings:  map[string]InputBinding{},
			outputBindings: map[string]OutputBinding{},
		}
	})
	return instance
}

func RegisterInputBinding(name string, binding InputBinding) {
	instance.inputBindings[fmt.Sprintf("bindings.%s", name)] = binding
}

func RegisterOutputBinding(name string, binding OutputBinding) {
	instance.outputBindings[fmt.Sprintf("bindings.%s", name)] = binding
}

func (b *bindingsRegistry) CreateInputBinding(name string) (InputBinding, error) {
	for key, binding := range b.inputBindings {
		if key == name {
			return binding, nil
		}
	}

	return nil, fmt.Errorf("couldn't find input binding %s", name)
}

func (b *bindingsRegistry) CreateOutputBinding(name string) (OutputBinding, error) {
	for key, binding := range b.outputBindings {
		if key == name {
			return binding, nil
		}
	}

	return nil, fmt.Errorf("couldn't find output binding %s", name)
}
