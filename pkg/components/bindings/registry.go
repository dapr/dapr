// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

import (
	"fmt"

	"github.com/dapr/components-contrib/bindings"
	"github.com/pkg/errors"
)

type (
	// InputBinding is an input binding component definition.
	InputBinding struct {
		Name          string
		FactoryMethod func() bindings.InputBinding
	}

	// OutputBinding is an output binding component definition.
	OutputBinding struct {
		Name          string
		FactoryMethod func() bindings.OutputBinding
	}

	// Registry is the interface of a components that allows callers to get registered instances of input and output bindings
	Registry interface {
		RegisterInputBindings(components ...InputBinding)
		RegisterOutputBindings(components ...OutputBinding)
		HasInputBinding(name, version string) bool
		HasOutputBinding(name, version string) bool
		CreateInputBinding(name, version string) (bindings.InputBinding, error)
		CreateOutputBinding(name, version string) (bindings.OutputBinding, error)
	}

	bindingsRegistry struct {
		inputBindings  map[string]func() bindings.InputBinding
		outputBindings map[string]func() bindings.OutputBinding
	}
)

// NewInput creates a InputBinding.
func NewInput(name string, factoryMethod func() bindings.InputBinding) InputBinding {
	return InputBinding{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewOutput creates a OutputBinding.
func NewOutput(name string, factoryMethod func() bindings.OutputBinding) OutputBinding {
	return OutputBinding{
		Name:          name,
		FactoryMethod: factoryMethod,
	}
}

// NewRegistry is used to create new bindings.
func NewRegistry() Registry {
	return &bindingsRegistry{
		inputBindings:  map[string]func() bindings.InputBinding{},
		outputBindings: map[string]func() bindings.OutputBinding{},
	}
}

// RegisterInputBindings registers one or more new input bindings.
func (b *bindingsRegistry) RegisterInputBindings(components ...InputBinding) {
	for _, component := range components {
		b.inputBindings[createFullName(component.Name)] = component.FactoryMethod
	}
}

// RegisterOutputBindings registers one or more new output bindings.
func (b *bindingsRegistry) RegisterOutputBindings(components ...OutputBinding) {
	for _, component := range components {
		b.outputBindings[createFullName(component.Name)] = component.FactoryMethod
	}
}

// Create instantiates an input binding based on `name`.
func (b *bindingsRegistry) CreateInputBinding(name, version string) (bindings.InputBinding, error) {
	if method, ok := b.getInputBinding(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find input binding %s/%s", name, version)
}

// Create instantiates an output binding based on `name`.
func (b *bindingsRegistry) CreateOutputBinding(name, version string) (bindings.OutputBinding, error) {
	if method, ok := b.getOutputBinding(name, version); ok {
		return method(), nil
	}
	return nil, errors.Errorf("couldn't find output binding %s/%s", name, version)
}

// HasInputBinding checks if an input binding based on `name` exists in the registry.
func (b *bindingsRegistry) HasInputBinding(name, version string) bool {
	_, ok := b.getInputBinding(name, version)
	return ok
}

// HasOutputBinding checks if an output binding based on `name` exists in the registry.
func (b *bindingsRegistry) HasOutputBinding(name, version string) bool {
	_, ok := b.getOutputBinding(name, version)
	return ok
}

func (b *bindingsRegistry) getInputBinding(name, version string) (func() bindings.InputBinding, bool) {
	binding, ok := b.inputBindings[name+"/"+version]
	if ok {
		return binding, true
	}
	if version == "" || version == "v0" || version == "v1" {
		binding, ok = b.inputBindings[name]
	}
	return binding, ok
}

func (b *bindingsRegistry) getOutputBinding(name, version string) (func() bindings.OutputBinding, bool) {
	bindingFn, ok := b.outputBindings[name+"/"+version]
	if ok {
		return bindingFn, true
	}
	if version == "" || version == "v0" || version == "v1" {
		bindingFn, ok = b.outputBindings[name]
	}
	return bindingFn, ok
}

func createFullName(name string) string {
	return fmt.Sprintf("bindings.%s", name)
}
