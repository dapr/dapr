/*
Copyright 2022 The Dapr Authors
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

var (
	log = logger.NewLogger("pluggable-components")
	// registries is a map from the given pluggable component type and its register.
	registries map[components.PluggableType]func(components.Pluggable)
)

// AddRegistryFor adds a new register function to the puggable registry.
func AddRegistryFor[T any](cmpType components.PluggableType, regFunc func(componentFactory func(logger.Logger) T, names ...string), factory func(logger.Logger, components.Pluggable) T) {
	registries[cmpType] = func(pc components.Pluggable) {
		regFunc(func(l logger.Logger) T {
			return factory(l, pc)
		}, pc.Name)
	}
}

// Register register all plugable components and returns the total number of registered components.
func Register(pcs ...components.Pluggable) int {
	registeredComponents := 0
	for _, pc := range pcs {
		register, ok := registries[pc.Type]
		if !ok {
			log.Warnf("%s is not registered as a pluggable component", pc.Type)
			continue
		}
		register(pc)
		registeredComponents++
		log.Infof("%s.%s %s pluggable component was successfully registered", pc.Type, pc.Name, pc.Version)
	}
	return registeredComponents
}

func init() {
	registries = make(map[components.PluggableType]func(components.Pluggable))
}
