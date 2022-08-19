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

package pluggable

import (
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

// Component represents a pluggable component specification.
type Component struct {
	// Name is the pluggable component name
	Name string
	// SocketPath is the component socket path
	SocketPath string
	// Type is the component type.
	Type string
}

var (
	log = logger.NewLogger("pluggable-components")

	// registry maintains a map on how to construct a component given a pluggable component
	registry = make(map[components.Type]func(Component) any)
)

// MustRegister registers a new pluggable component into the map and panics if a component for the same type was already registered
func MustRegister[T any](
	compType components.Type,
	new func(Component) T,
) {
	if _, ok := registry[compType]; ok {
		log.Fatalf("a pluggable component for the type (%s) has already been registered", compType)
	}
	registry[compType] = func(pc Component) any {
		return new(pc)
	}
}

// MustLoad loads a pluggable component that was registered before
func MustLoad[T any](pc Component) T {
	loader, ok := registry[components.Type(pc.Type)]
	if !ok {
		log.Fatalf("%s not registered as a pluggable component", pc.Type)
	}
	anyInstance := loader(pc)

	instance, ok := anyInstance.(T)
	if !ok {
		log.Fatalf("%s has tried to load as #v but type does not match", pc.Type, anyInstance)
	}

	return instance
}
