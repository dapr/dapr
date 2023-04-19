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

package injector

import (
	"fmt"

	"golang.org/x/sync/singleflight"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/components"
)

// namespaceFlight deduplicates requests to the same namespace
var namespaceFlight singleflight.Group

func (i *injector) splitContainers(pod corev1.Pod) (appContainers map[int]corev1.Container, componentContainers map[int]corev1.Container, injectedComponentContainers []corev1.Container, err error) {
	an := annotations.New(pod.Annotations)
	injectionEnabled := an.GetBoolOrDefault(annotations.KeyPluggableComponentsInjection, false)
	if injectionEnabled {
		// FIXME There is a potential issue with the components being fetched from the operator versus at runtime.
		// This would lead in two possible scenarios:
		// 1) If the component is not listed here but listed in runtime,
		// you may encounter runtime errors related to the missing container for the component's bootstrapping process.
		// However, due to Kubernetes' reconciling approach, this issue should resolve itself over time.
		// 2) If the component is listed here but not listed in runtime,
		// the pod will include a redundant container that should be removed when this code is executed again.
		// To resolve this issue, it is important to ensure that the same component list is used both here and in runtime,
		// such as by passing it as an environment variable or populating a volume with an init container.
		componentsList, err, _ := namespaceFlight.Do(pod.Namespace, func() (any, error) {
			return i.daprClient.ComponentsV1alpha1().Components(pod.Namespace).List(metav1.ListOptions{})
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("error when fetching components: %w", err)
		}
		injectedComponentContainers = components.Injectable(an.GetString(annotations.KeyAppID), componentsList.(*v1alpha1.ComponentList).Items)
	}
	appContainers, componentContainers = components.SplitContainers(pod)
	return
}
