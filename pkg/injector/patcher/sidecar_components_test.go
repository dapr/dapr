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

package patcher

import (
	"encoding/json"
	"fmt"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
)

func TestComponentsPatch(t *testing.T) {
	const appName, componentImage, componentName = "my-app", "my-image", "my-component"
	socketSharedVolumeMount := sharedComponentsUnixSocketVolumeMount(injectorConsts.ComponentsUDSDefaultFolder)
	appContainer := corev1.Container{
		Name: "app",
	}
	testCases := []struct {
		name           string
		appID          string
		componentsList []componentsapi.Component
		pod            *corev1.Pod
		expPatch       jsonpatch.Patch
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
			jsonpatch.Patch{},
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
			jsonpatch.Patch{
				NewPatchOperation("add", PatchPathVolumes, []corev1.Volume{sharedComponentsSocketVolume()}),
				NewPatchOperation("add", PatchPathContainers+"/1/env", []corev1.EnvVar{{
					Name:  injectorConsts.ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				NewPatchOperation("add", PatchPathContainers+"/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
			},
			&socketSharedVolumeMount,
		},
		{
			"patch should not create injectable containers operations when app is scopped but has no annotations",
			appName,
			[]componentsapi.Component{
				{
					Scoped: commonapi.Scoped{Scopes: []string{appName}},
				},
			},
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
			jsonpatch.Patch{
				NewPatchOperation("add", PatchPathVolumes, []corev1.Volume{sharedComponentsSocketVolume()}),
				NewPatchOperation("add", PatchPathContainers+"/1/env", []corev1.EnvVar{{
					Name:  injectorConsts.ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				NewPatchOperation("add", PatchPathContainers+"/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
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
						annotations.KeyPluggableComponentContainer: fmt.Sprintf(`{
							"image": "%s",
							"env": [{"name": "A", "value": "B"}],
							"volumeMounts": [{"mountPath": "/read-only", "name": "readonly", "readOnly": true}, {"mountPath": "/read-write", "name": "readwrite", "readOnly": false}]
						}`, componentImage),
					},
				},
				Scoped: commonapi.Scoped{Scopes: []string{appName}},
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
			jsonpatch.Patch{
				NewPatchOperation("add", PatchPathVolumes, []corev1.Volume{sharedComponentsSocketVolume()}),
				NewPatchOperation("add", PatchPathContainers+"/1/env", []corev1.EnvVar{{
					Name:  injectorConsts.ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				NewPatchOperation("add", PatchPathContainers+"/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
				NewPatchOperation("add", PatchPathVolumes+"/-", corev1.Volume{
					Name: "readonly",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				}),
				NewPatchOperation("add", PatchPathVolumes+"/-", corev1.Volume{
					Name: "readwrite",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				}),
				NewPatchOperation("add", PatchPathContainers+"/-", corev1.Container{
					Name:  componentName,
					Image: componentImage,
					Env: []corev1.EnvVar{
						{
							Name:  "A",
							Value: "B",
						},
						{
							Name:  injectorConsts.ComponentsUDSMountPathEnvVar,
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
				}),
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
			jsonpatch.Patch{
				NewPatchOperation("add", PatchPathVolumes+"/-", sharedComponentsSocketVolume()),
				NewPatchOperation("add", PatchPathContainers+"/1/env", []corev1.EnvVar{{
					Name:  injectorConsts.ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				NewPatchOperation("add", PatchPathContainers+"/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
			},
			&socketSharedVolumeMount,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			c := NewSidecarConfig(test.pod)
			c.SetFromPodAnnotations()
			_, componentContainers := c.splitContainers()
			patch, volumeMount := c.componentsPatchOps(componentContainers, Injectable(test.appID, test.componentsList))
			patchJSON, _ := json.Marshal(patch)
			expPatchJSON, _ := json.Marshal(test.expPatch)
			assert.Equal(t, string(expPatchJSON), string(patchJSON))
			assert.Equal(t, test.expMount, volumeMount)
		})
	}
}
