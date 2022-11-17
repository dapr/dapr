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
	"strings"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Injectable takes a pod as an argument and returns all injectable components containers to that pod,
func Injectable(pod corev1.Pod, daprClient scheme.Interface) ([]corev1.Container, error) {
	an := sidecar.Annotations(pod.Annotations)
	injectionEnabled := an.GetBoolOrDefault(annotations.KeyPluggableComponentsInjection, false)
	if !injectionEnabled {
		return []corev1.Container{}, nil
	}

	// FIXME there are racing conditions between components being fetched from the operator in runtime and this.
	// would lead in two possible scenarios:
	// 1) The component is not listed here but listed in runtime:
	// you'll probably see runtime errors related to the component bootstrapping as the required container is missing,
	// but because the nature of kuberentes reconciling approach this will start working at some point.
	// 2) The component is listed here but not listed in runtime:
	// the pod will add a useless container that should be removed when this code execute again for any reason.
	componentsList, err := daprClient.ComponentsV1alpha1().Components(pod.Namespace).List(metav1.ListOptions{})

	if err != nil {
		return nil, errors.Wrap(err, "error reading components")
	}

	return buildComponentContainers(an.GetString(annotations.KeyAppID), componentsList.Items), nil
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

		typeName := strings.Split(component.Spec.Type, ".")
		if len(typeName) < 2 { // invalid name
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
