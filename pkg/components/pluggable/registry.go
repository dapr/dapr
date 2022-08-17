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
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/kit/logger"
)

var (
	log = logger.NewLogger("pluggable-components")

	// registry maintains a map on how to construct a component given a pluggable component
	registry = make(map[components.Type]func(components_v1alpha1.PluggableComponent) any)
)

// Register register a new pluggable component into the map
func Register[T any](
	compType components.Type,
	new func(components_v1alpha1.PluggableComponent) T,
) {
	registry[compType] = func(pc components_v1alpha1.PluggableComponent) any {
		return new(pc)
	}
}

// MustLoad loads a pluggable component that was registered before
func MustLoad[T any](pc components_v1alpha1.PluggableComponent) T {
	loader, ok := registry[components.Type(pc.Spec.Type)]
	if !ok {
		log.Fatalf("%s not registered as a pluggable component", pc.Spec.Type)
	}
	anyInstance := loader(pc)

	instance, ok := anyInstance.(T)
	if !ok {
		log.Fatalf("%s has tried to load as #v but type does not match", pc.Spec.Type, anyInstance)
	}

	return instance
}
