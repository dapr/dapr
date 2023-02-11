/*
Copyright 2023 The Dapr Authors
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
	"encoding/json"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/kit/logger"

	corev1 "k8s.io/api/core/v1"
)

var log = logger.NewLogger("dapr.injector.components")

// Injectable parses the container definition from components annotations returning them as a list. Uses the appID to filter
// only the eligble components for such apps avoiding injecting containers that will not be used.
func Injectable(appID string, components []componentsapi.Component) []corev1.Container {
	componentContainers := make([]corev1.Container, 0)
	componentImages := make(map[string]bool, 0)

	for _, component := range components {
		containerAsStr := component.Annotations[annotations.KeyPluggableComponentContainer]
		if containerAsStr == "" {
			continue
		}
		var container *corev1.Container
		if err := json.Unmarshal([]byte(containerAsStr), &container); err != nil {
			log.Warnf("could not unmarshal %s error: %w", component.Name, err)
			continue
		}

		if componentImages[container.Image] {
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
			componentImages[container.Image] = true
			// if container name is not set, the component name will be used ensuring uniqueness
			if container.Name == "" {
				container.Name = component.Name
			}
			componentContainers = append(componentContainers, *container)
		}
	}

	return componentContainers
}
