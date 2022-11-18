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
	componentsv1alpha1 "github.com/dapr/dapr/pkg/client/clientset/versioned/typed/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Injectable takes the component list and an appID and returns all injectable component containers for the given app.
func Injectable(appID string, componentsClient componentsv1alpha1.ComponentInterface) ([]corev1.Container, error) {
	// FIXME there could be a inconsistency between components being fetched from the operator in runtime and this.
	// would lead in two possible scenarios:
	// 1) The component is not listed here but listed in runtime:
	// you'll probably see runtime errors related to the component bootstrapping as the required container is missing,
	// but because the nature of kuberentes reconciling approach this will start working at some point.
	// 2) The component is listed here but not listed in runtime:
	// the pod will add a useless container that should be removed when this code execute again for any reason.
	// a possible fix would be making sure the same list here will be the same list used in runtime.
	// e.g passing as a environment variable or populating a volume using an init container.
	componentsList, err := componentsClient.List(metav1.ListOptions{})

	if err != nil {
		return nil, errors.Wrap(err, "error reading components")
	}

	return buildComponentContainers(appID, componentsList.Items), nil
}

// buildComponentContainers returns the component containters for the given app ID.
func buildComponentContainers(appID string, components []componentsapi.Component) []corev1.Container {
	componentContainers := make([]corev1.Container, 0)
	componentImages := make(map[string]bool, 0)
	containersImagesStr := make([]string, 0)

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
			mounts := append(readonlyMounts, rwMounts...)
			componentImages[containerImage] = true
			containersImagesStr = append(containersImagesStr, containerImage)
			componentContainers = append(componentContainers, corev1.Container{
				Name:         component.Name,
				Image:        containerImage,
				Env:          sidecar.ParseEnvString(component.Annotations[annotations.KeyPluggableComponentContainerEnvironment]),
				VolumeMounts: mounts,
			})
		}
	}

	return componentContainers
}
