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

package components

import "fmt"

// PluggableType is the component type.
type PluggableType string

const (
	State          PluggableType = "state"
	PubSub         PluggableType = "pubsub"
	InputBinding   PluggableType = "inputbinding"
	OutputBinding  PluggableType = "outputbinding"
	HTTPMiddleware PluggableType = "middleware.http"
	Configuration  PluggableType = "configuration"
	Secret         PluggableType = "secret"
	Lock           PluggableType = "lock"
	NameResolution PluggableType = "nameresolution"
)

// WellKnownTypes is used as a handy way to iterate over all possibles component type.
var WellKnownTypes = [9]PluggableType{
	State,
	PubSub,
	InputBinding,
	OutputBinding,
	HTTPMiddleware,
	Configuration,
	Secret,
	Lock,
	NameResolution,
}

var pluggableToCategory = map[PluggableType]Category{
	State:          CategoryStateStore,
	PubSub:         CategoryPubSub,
	InputBinding:   CategoryBindings,
	OutputBinding:  CategoryBindings,
	HTTPMiddleware: CategoryMiddleware,
	Configuration:  CategoryConfiguration,
	Secret:         CategorySecretStore,
	Lock:           CategoryLock,
	NameResolution: CategoryNameResolution,
}

// Pluggable represents a pluggable component specification.
type Pluggable struct {
	// Name is the pluggable component name.
	Name string
	// Type is the component type.
	Type PluggableType
	// Version is the pluggable component version.
	Version string
}

// Category returns the component category based on its pluggable type.
func (p Pluggable) Category() Category {
	return pluggableToCategory[p.Type]
}

// FullName returns the full component name including its version.
// i.e for `name: custom-store`, `version: v1` its full name will be `custom-store/v1`.
// when version not specified only the component name will be used.
func (p Pluggable) FullName() string {
	// when version is not specified so the component name will be returned.
	if p.Version == "" {
		return p.Name
	}
	return fmt.Sprintf("%s/%s", p.Name, p.Version)
}
