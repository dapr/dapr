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
	"testing"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestComponentsPatch(t *testing.T) {
	socketSharedVolumeMount := sharedComponentsUnixSocketVolumeMount("/tmp/dapr-components-sockets")
	appContainer := corev1.Container{
		Name: "app",
	}
	testCases := []struct {
		name     string
		pod      *corev1.Pod
		expPatch []sidecar.PatchOperation
		expMount *corev1.VolumeMount
	}{
		{
			"patch should return pod containers and empty patch operations when none pluggable components are specified",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{appContainer},
				},
			},
			[]sidecar.PatchOperation{},
			nil,
		},
		{
			"patch should create pluggable component unix socket volume",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyPluggableComponents: "component",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{appContainer, {
						Name: "component",
					}},
				},
			},
			[]sidecar.PatchOperation{
				{
					Op:    "add",
					Path:  sidecar.VolumesPath,
					Value: []corev1.Volume{sharedComponentsSocketVolume()},
				},
				{
					Op:   "add",
					Path: "/spec/containers/1/env",
					Value: []corev1.EnvVar{{
						Name:  componentsUnixDomainSocketMountPathEnvVar,
						Value: socketSharedVolumeMount.MountPath,
					}},
				},
				{
					Op:    "add",
					Path:  "/spec/containers/1/volumeMounts",
					Value: []corev1.VolumeMount{socketSharedVolumeMount},
				},
			},
			&socketSharedVolumeMount,
		},
		{
			"patch should add pluggable component unix socket volume when pod already has volumes",
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyPluggableComponents: "component",
					},
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{{}},
					Containers: []corev1.Container{appContainer, {
						Name: "component",
					}},
				},
			},
			[]sidecar.PatchOperation{
				{
					Op:    "add",
					Path:  sidecar.VolumesPath + "/-",
					Value: sharedComponentsSocketVolume(),
				},
				{
					Op:   "add",
					Path: "/spec/containers/1/env",
					Value: []corev1.EnvVar{{
						Name:  componentsUnixDomainSocketMountPathEnvVar,
						Value: socketSharedVolumeMount.MountPath,
					}},
				},
				{
					Op:    "add",
					Path:  "/spec/containers/1/volumeMounts",
					Value: []corev1.VolumeMount{socketSharedVolumeMount},
				},
			},
			&socketSharedVolumeMount,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			_, componentContainers := SplitContainers(*test.pod)
			patch, volumeMount := PatchOps(componentContainers, test.pod)
			assert.Equal(t, patch, test.expPatch)
			assert.Equal(t, volumeMount, test.expMount)
		})
	}
}
