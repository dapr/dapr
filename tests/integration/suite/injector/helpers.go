/*
Copyright 2026 The Dapr Authors
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
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildPod returns a JSON-encoded pod with the given name and annotations.
func buildPod(name string, annotations map[string]string) []byte {
	return buildPodWithContainers(name, annotations, []corev1.Container{
		{Name: "main", Image: "docker.io/app:latest"},
	})
}

// buildPodWithContainers returns a JSON-encoded pod with custom containers.
func buildPodWithContainers(name string, annotations map[string]string, containers []corev1.Container) []byte {
	pod := corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Annotations: annotations,
			Labels:      map[string]string{"app": name},
		},
		Spec: corev1.PodSpec{
			Containers: containers,
		},
	}
	b, _ := json.Marshal(pod)
	return b
}
