package injector

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
	"github.com/dapr/dapr/pkg/injector/patcher"
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
				patcher.NewPatchOperation("add", patcher.PatchPathVolumes, []corev1.Volume{sharedComponentsSocketVolume()}),
				patcher.NewPatchOperation("add", "/spec/containers/1/env", []corev1.EnvVar{{
					Name:  ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				patcher.NewPatchOperation("add", "/spec/containers/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
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
				patcher.NewPatchOperation("add", patcher.PatchPathVolumes, []corev1.Volume{sharedComponentsSocketVolume()}),
				patcher.NewPatchOperation("add", "/spec/containers/1/env", []corev1.EnvVar{{
					Name:  ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				patcher.NewPatchOperation("add", "/spec/containers/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
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
				patcher.NewPatchOperation("add", patcher.PatchPathVolumes, []corev1.Volume{sharedComponentsSocketVolume()}),
				patcher.NewPatchOperation("add", "/spec/containers/1/env", []corev1.EnvVar{{
					Name:  ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				patcher.NewPatchOperation("add", "/spec/containers/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
				patcher.NewPatchOperation("add", patcher.PatchPathVolumes+"/-", corev1.Volume{
					Name: "readonly",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				}),
				patcher.NewPatchOperation("add", patcher.PatchPathVolumes+"/-", corev1.Volume{
					Name: "readwrite",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				}),
				patcher.NewPatchOperation("add", patcher.PatchPathContainers+"/-", corev1.Container{
					Name:  componentName,
					Image: componentImage,
					Env: []corev1.EnvVar{
						{
							Name:  "A",
							Value: "B",
						},
						{
							Name:  ComponentsUDSMountPathEnvVar,
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
				patcher.NewPatchOperation("add", patcher.PatchPathVolumes+"/-", []corev1.Volume{sharedComponentsSocketVolume()}),
				patcher.NewPatchOperation("add", "/spec/containers/1/env", []corev1.EnvVar{{
					Name:  ComponentsUDSMountPathEnvVar,
					Value: socketSharedVolumeMount.MountPath,
				}}),
				patcher.NewPatchOperation("add", "/spec/containers/1/volumeMounts", []corev1.VolumeMount{socketSharedVolumeMount}),
			},
			&socketSharedVolumeMount,
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			c := NewSidecarConfig(test.pod)
			_, componentContainers := c.SplitContainers()
			patch, volumeMount := c.ComponentsPatchOps(componentContainers, Injectable(test.appID, test.componentsList))
			patchJSON, _ := json.Marshal(patch)
			expPatchJSON, _ := json.Marshal(patch)
			assert.Equal(t, string(patchJSON), string(expPatchJSON))
			assert.Equal(t, volumeMount, test.expMount)
		})
	}
}
