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

// SocketPathForPluggableComponent returns the desired socket path for the given pluggable component.
func SocketPathForPluggableComponent(name, version string) string {
	versionSuffix := fmt.Sprintf("-%s", version)
	if version == "" {
		versionSuffix = ""
	}
	return fmt.Sprintf("%s/dapr-%s%s.sock", GetPluggableComponentsSocketFolderPath(), name, versionSuffix)
}
