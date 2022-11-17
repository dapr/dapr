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

	"github.com/dapr/dapr/pkg/injector/components"
)

func (i *injector) splitContainers(pod corev1.Pod) (appContainers map[int]corev1.Container, componentContainers map[int]corev1.Container, injectedComponentContainers []corev1.Container, err error) {
	injectedComponentContainers, err = components.Injectable(pod, i.daprClient)
	appContainers, componentContainers = components.SplitContainers(pod)
	return
}
