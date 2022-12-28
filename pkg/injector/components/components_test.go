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

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/common"
	"github.com/dapr/dapr/pkg/injector/sidecar"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestComponentsPatch(t *testing.T) {
	const appName, componentImage, componentName = "my-app", "my-image", "my-component"
	socketSharedVolumeMount := sharedComponentsUnixSocketVolumeMount("/tmp/dapr-components-sockets")
	appContainer := corev1.Container{
		Name: "app",
	}
	testCases := []struct {
		name           string
		appID          string
		componentsList []componentsapi.Component
		pod            *corev1.Pod
		expPatch       []common.PatchOperation
		expMount       *corev1.VolumeMount
	}{
		{
			"patch should return empty patch operations when none pluggable components are specified",
			"",
			[]componentsapi.Component{},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{appContainer},
				},
			},
			[]common.PatchOperation{},
			nil,
		},
		{
			"patch should create pluggable component unix socket volume",
			"",
			[]componentsapi.Component{},
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
			[]common.PatchOperation{
				{
					Op:    "add",
					Path:  sidecar.PatchPathVolumes,
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
			"patch should not create injectable containers operations when app is scopped but has no annotations",
			appName,
			[]componentsapi.Component{{
				Scopes: []string{appName},
			}},
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
			[]common.PatchOperation{
				{
					Op:    "add",
					Path:  sidecar.PatchPathVolumes,
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
			"patch should create injectable containers operations when app is scopped but and has annotations",
			appName,
			[]componentsapi.Component{{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName,
					Annotations: map[string]string{
						annotations.KeyPluggableComponentContainerImage:                 componentImage,
						annotations.KeyPluggableComponentContainerVolumeMountsReadOnly:  "readonly:/read-only",
						annotations.KeyPluggableComponentContainerVolumeMountsReadWrite: "readwrite:/read-write",
						annotations.KeyPluggableComponentContainerEnvironment:           "A=B",
					},
				},
				Scopes: []string{appName},
			}},
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
			[]common.PatchOperation{
				{
					Op:    "add",
					Path:  sidecar.PatchPathVolumes,
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
				{
					Op:   "add",
					Path: sidecar.PatchPathVolumes + "/-",
					Value: corev1.Volume{
						Name: "readonly",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
				{
					Op:   "add",
					Path: sidecar.PatchPathVolumes + "/-",
					Value: corev1.Volume{
						Name: "readwrite",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
				{
					Op:   "add",
					Path: "/spec/containers/-",
					Value: corev1.Container{
						Name:  componentName,
						Image: componentImage,
						Env: []corev1.EnvVar{
							{
								Name:  "A",
								Value: "B",
							},
							{
								Name:  componentsUnixDomainSocketMountPathEnvVar,
								Value: socketSharedVolumeMount.MountPath,
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "readonly",
								ReadOnly:  true,
								MountPath: "/read-only",
							},
							{
								Name:      "readwrite",
								ReadOnly:  false,
								MountPath: "/read-write",
							},
							socketSharedVolumeMount,
						},
					},
				},
			},
			&socketSharedVolumeMount,
		},
		{
			"patch should add pluggable component unix socket volume when pod already has volumes",
			"",
			[]componentsapi.Component{},
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
			[]common.PatchOperation{
				{
					Op:    "add",
					Path:  sidecar.PatchPathVolumes + "/-",
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
			patch, volumeMount := PatchOps(componentContainers, Injectable(test.appID, test.componentsList), test.pod)
			assert.Equal(t, patch, test.expPatch)
			assert.Equal(t, volumeMount, test.expMount)
		})
	}
}
