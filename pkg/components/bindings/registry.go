/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package bindings

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Registry is the interface of a components that allows callers to get registered instances of input and output bindings.
type Registry struct {
	Logger         logger.Logger
	inputBindings  map[string]func(logger.Logger) bindings.InputBinding
	outputBindings map[string]func(logger.Logger) bindings.OutputBinding
}

// DefaultRegistry is the singleton with the registry.
var DefaultRegistry *Registry = NewRegistry()

// NewRegistry is used to create new bindings.
func NewRegistry() *Registry {
	return &Registry{
		inputBindings:  map[string]func(logger.Logger) bindings.InputBinding{},
		outputBindings: map[string]func(logger.Logger) bindings.OutputBinding{},
	}
}

// RegisterInputBinding adds a name input binding to the registry.
func (b *Registry) RegisterInputBinding(componentFactory func(logger.Logger) bindings.InputBinding, names ...string) {
	for _, name := range names {
		b.inputBindings[createFullName(name)] = componentFactory
	}
}

// RegisterOutputBinding adds a name output binding to the registry.
func (b *Registry) RegisterOutputBinding(componentFactory func(logger.Logger) bindings.OutputBinding, names ...string) {
	for _, name := range names {
		b.outputBindings[createFullName(name)] = componentFactory
	}
}

// CreateInputBinding Create instantiates an input binding based on `name`.
func (b *Registry) CreateInputBinding(name, version, logName string) (bindings.InputBinding, error) {
	if method, ok := b.getInputBinding(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find input binding %s/%s", name, version)
}

// CreateOutputBinding Create instantiates an output binding based on `name`.
func (b *Registry) CreateOutputBinding(name, version, logName string) (bindings.OutputBinding, error) {
	if method, ok := b.getOutputBinding(name, version, logName); ok {
		return method(), nil
	}
	return nil, fmt.Errorf("couldn't find output binding %s/%s", name, version)
}

// HasInputBinding checks if an input binding based on `name` exists in the registry.
func (b *Registry) HasInputBinding(name, version string) bool {
	_, ok := b.getInputBinding(name, version, "")
	return ok
}

// HasOutputBinding checks if an output binding based on `name` exists in the registry.
func (b *Registry) HasOutputBinding(name, version string) bool {
	_, ok := b.getOutputBinding(name, version, "")
	return ok
}

func (b *Registry) getInputBinding(name, version, logName string) (func() bindings.InputBinding, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	bindingFn, ok := b.inputBindings[nameLower+"/"+versionLower]
	if ok {
		return b.wrapInputBindingFn(bindingFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		bindingFn, ok = b.inputBindings[nameLower]
		if ok {
			return b.wrapInputBindingFn(bindingFn, logName), true
		}
	}

	return nil, false
}

func (b *Registry) getOutputBinding(name, version, logName string) (func() bindings.OutputBinding, bool) {
	nameLower := strings.ToLower(name)
	versionLower := strings.ToLower(version)
	bindingFn, ok := b.outputBindings[nameLower+"/"+versionLower]
	if ok {
		return b.wrapOutputBindingFn(bindingFn, logName), true
	}
	if components.IsInitialVersion(versionLower) {
		bindingFn, ok = b.outputBindings[nameLower]
		if ok {
			return b.wrapOutputBindingFn(bindingFn, logName), true
		}
	}

	return nil, false
}

func (b *Registry) wrapInputBindingFn(componentFactory func(logger.Logger) bindings.InputBinding, logName string) func() bindings.InputBinding {
	return func() bindings.InputBinding {
		l := b.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(l)
	}
}

func (b *Registry) wrapOutputBindingFn(componentFactory func(logger.Logger) bindings.OutputBinding, logName string) func() bindings.OutputBinding {
	return func() bindings.OutputBinding {
		l := b.Logger
		if logName != "" && l != nil {
			l = l.WithFields(map[string]any{
				"component": logName,
			})
		}
		return componentFactory(l)
	}
}

func createFullName(name string) string {
	return strings.ToLower("bindings." + name)
}
