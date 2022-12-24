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
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"

	corev1 "k8s.io/api/core/v1"
)

// buildComponentContainers returns the component containers for the given app ID.
func Injectable(appID string, components []componentsapi.Component) []corev1.Container {
	componentContainers := make([]corev1.Container, 0)
	componentImages := make(map[string]bool, 0)

	for _, component := range components {
		containerImage := component.Annotations[annotations.KeyPluggableComponentContainerImage]
		if containerImage == "" || componentImages[containerImage] {
			continue
		}

		appScopped := len(component.Scopes) == 0
		for _, scoppedApp := range component.Scopes {
			if scoppedApp == appID {
				appScopped = true
				break
			}
		}

		if appScopped {
			readonlyMounts := sidecar.ParseVolumeMountsString(component.Annotations[annotations.KeyPluggableComponentContainerVolumeMountsReadOnly], true)
			rwMounts := sidecar.ParseVolumeMountsString(component.Annotations[annotations.KeyPluggableComponentContainerVolumeMountsReadWrite], false)
			componentImages[containerImage] = true
			componentContainers = append(componentContainers, corev1.Container{
				Name:         component.Name,
				Image:        containerImage,
				Env:          sidecar.ParseEnvString(component.Annotations[annotations.KeyPluggableComponentContainerEnvironment]),
				VolumeMounts: append(readonlyMounts, rwMounts...),
			})
		}
	}

	return componentContainers
}
