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

package injector

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/components"
	"github.com/dapr/dapr/pkg/injector/sidecar"

	"github.com/pkg/errors"

	"golang.org/x/sync/singleflight"
)

// namespaceFlight deduplicates requests for the same namespace
var namespaceFlight singleflight.Group

func (i *injector) splitContainers(pod corev1.Pod) (appContainers map[int]corev1.Container, componentContainers map[int]corev1.Container, injectedComponentContainers []corev1.Container, err error) {
	an := sidecar.Annotations(pod.Annotations)
	injectionEnabled := an.GetBoolOrDefault(annotations.KeyPluggableComponentsInjection, false)
	if injectionEnabled {
		// FIXME there could be a inconsistency between components being fetched from the operator in runtime and this.
		// would lead in two possible scenarios:
		// 1) The component is not listed here but listed in runtime:
		// you'll probably see runtime errors related to the component bootstrapping as the required container is missing,
		// but because the nature of kuberentes reconciling approach this will start working at some point.
		// 2) The component is listed here but not listed in runtime:
		// the pod will add a useless container that should be removed when this code execute again for any reason.
		// a possible fix would be making sure the same list here will be the same list used in runtime.
		// e.g passing as a environment variable or populating a volume using an init container.
		componentsList, err, _ := namespaceFlight.Do(pod.Namespace, func() (any, error) {
			return i.daprClient.ComponentsV1alpha1().Components(pod.Namespace).List(metav1.ListOptions{})
		})
		if err != nil {
			return nil, nil, nil, errors.Wrap(err, "error reading components")
		}
		injectedComponentContainers = components.Injectable(an.GetString(annotations.KeyAppID), componentsList.(*v1alpha1.ComponentList).Items)
	}
	appContainers, componentContainers = components.SplitContainers(pod)
	return
}
