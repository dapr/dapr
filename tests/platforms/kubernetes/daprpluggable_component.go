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

package kubernetes

import (
	"fmt"

	"github.com/dapr/dapr/pkg/components/pluggable"
	"github.com/dapr/dapr/pkg/injector/sidecar"

	apiv1 "k8s.io/api/core/v1"
)

const (
	// pluggableComponentSocketEnvVar env var used to point the socket location for the pluggable component.
	pluggableComponentSocketEnvVar    = "DAPR_COMPONENT_SOCKET_FOLDER"
	pluggableComponentSocketMountPath = "/dapr-unix-domain-sockets"
)

// sharedUnixSocketVolume creates a shared unix socket volume to be used by sidecar.
func sharedUnixSocketVolume() apiv1.Volume {
	return apiv1.Volume{
		Name: sidecar.UnixDomainSocketVolume,
		VolumeSource: apiv1.VolumeSource{
			EmptyDir: &apiv1.EmptyDirVolumeSource{},
		},
	}
}

// sharedUnixSocketVolume creates a mount unix socket volume to be used for the pluggable component container.
func sharedUnixSocketVolumeMount() apiv1.VolumeMount {
	return apiv1.VolumeMount{
		Name:      sidecar.UnixDomainSocketVolume,
		MountPath: pluggableComponentSocketMountPath,
	}
}

// daprSocketPathEnvVar returns a env var that points to the pluggable component socket path environment variable.
func daprSocketPathEnvVar() apiv1.EnvVar {
	return apiv1.EnvVar{
		Name:  pluggableComponentSocketEnvVar,
		Value: pluggableComponentSocketMountPath,
	}
}

// adaptAndBuildPluggableComponents returns the additional pluggable components containers for the given app.
// it also hydrate the appdesc with the required volumes and socket variables.
func adaptAndBuildPluggableComponents(appDesc *AppDescription) []apiv1.Container {
	// add shared volume
	appDesc.Volumes = append(appDesc.Volumes, sharedUnixSocketVolume())

	// specify unix domain socket path
	if appDesc.UnixDomainSocketPath == "" {
		appDesc.UnixDomainSocketPath = pluggable.GetSocketFolderPath()
	} else {
		// if specified so the env var should be set.
		sidecarSocketFolderEnvVar := fmt.Sprintf("%s=%s", pluggable.SocketFolderEnvVar, appDesc.UnixDomainSocketPath)
		if appDesc.DaprEnv == "" {
			appDesc.DaprEnv = sidecarSocketFolderEnvVar
		} else {
			appDesc.DaprEnv = fmt.Sprintf("%s,%s", appDesc.DaprEnv, sidecarSocketFolderEnvVar)
		}
	}

	// each socket should point to a container.
	containers := make([]apiv1.Container, len(appDesc.PluggableComponents))
	containerIdx := 0
	sharedVolumeMount := sharedUnixSocketVolumeMount()
	for _, container := range appDesc.PluggableComponents {
		container.VolumeMounts = append(container.VolumeMounts, sharedVolumeMount)
		container.Env = append(container.Env, daprSocketPathEnvVar())
		containers[containerIdx] = container
		containerIdx++
	}
	return containers
}
