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

import (
	"fmt"

	"github.com/dapr/dapr/utils"
)

const (
	DaprPluggableComponentsSocketFolderEnvVar = "DAPR_PLUGGABLE_COMPONENTS_SOCKETS_FOLDER"
	defaultSocketFolder                       = "/var/run"
)

// GetPluggableComponentsSocketFolderPath returns the shared unix domain socket folder path
func GetPluggableComponentsSocketFolderPath() string {
	return utils.GetEnvOrElse(DaprPluggableComponentsSocketFolderEnvVar, defaultSocketFolder)
}

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
func (pc Pluggable) Category() Category {
	return pluggableToCategory[pc.Type]
}

// SocketPath returns the desired socket path for the given pluggable component.
func (pc Pluggable) SocketPath() string {
	versionSuffix := fmt.Sprintf("-%s", pc.Version)
	if pc.Version == "" {
		versionSuffix = ""
	}
	return fmt.Sprintf("%s/dapr-%s.%s%s.sock", GetPluggableComponentsSocketFolderPath(), pc.Category(), pc.Name, versionSuffix)
}
