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

	"github.com/dapr/dapr/pkg/components/pluggable"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/patcher"

	corev1 "k8s.io/api/core/v1"
)

const (
	componentsUnixDomainSocketVolumeName      = "dapr-components-unix-domain-socket" // Name of the Unix domain socket volume for components.
	componentsUnixDomainSocketMountPathEnvVar = "DAPR_COMPONENT_SOCKETS_FOLDER"
)

// sharedComponentsSocketVolume creates a shared unix socket volume to be used by sidecar.
func sharedComponentsSocketVolume() corev1.Volume {
	return corev1.Volume{
		Name: componentsUnixDomainSocketVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

// sharedComponentsUnixSocketVolumeMount creates a shared unix socket volume mount to be used by pluggable component.
func sharedComponentsUnixSocketVolumeMount(mountPath string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      componentsUnixDomainSocketVolumeName,
		MountPath: mountPath,
	}
}

// SplitContainers split containers between appContainers and componentContainers.
func SplitContainers(pod corev1.Pod) (appContainers map[int]corev1.Container, componentContainers map[int]corev1.Container) {
	appContainers = make(map[int]corev1.Container, len(pod.Spec.Containers))
	componentContainers = make(map[int]corev1.Container, len(pod.Spec.Containers))
	pluggableComponents := annotations.New(pod.Annotations).GetString(annotations.KeyPluggableComponents)
	componentsNames := strings.Split(pluggableComponents, ",")
	isComponent := make(map[string]bool, len(componentsNames))
	for _, name := range componentsNames {
		isComponent[name] = true
	}

	for idx, container := range pod.Spec.Containers {
		if isComponent[container.Name] {
			componentContainers[idx] = container
		} else {
			appContainers[idx] = container
		}
	}

	return appContainers, componentContainers
}

// PatchOps returns the patch operations required to properly bootstrap the pluggable component and the respective volume mount for the sidecar.
func PatchOps(componentContainers map[int]corev1.Container, injectedContainers []corev1.Container, pod *corev1.Pod) ([]patcher.PatchOperation, *corev1.VolumeMount) {
	patches := make([]patcher.PatchOperation, 0)

	if len(componentContainers) == 0 && len(injectedContainers) == 0 {
		return patches, nil
	}

	podAnnotations := annotations.New(pod.Annotations)
	mountPath := podAnnotations.GetString(annotations.KeyPluggableComponentsSocketsFolder)
	if mountPath == "" {
		mountPath = pluggable.GetSocketFolderPath()
	}

	volumePatch, sharedSocketVolumeMount := addSharedSocketVolume(mountPath, pod)
	patches = append(patches, volumePatch)
	componentsEnvVars := []corev1.EnvVar{{
		Name:  componentsUnixDomainSocketMountPathEnvVar,
		Value: sharedSocketVolumeMount.MountPath,
	}}

	for idx, container := range componentContainers {
		patches = append(patches, patcher.GetEnvPatchOperations(container.Env, componentsEnvVars, idx)...)
		patches = append(patches, patcher.GetVolumeMountPatchOperations(container.VolumeMounts, []corev1.VolumeMount{sharedSocketVolumeMount}, idx)...)
	}

	podVolumes := make(map[string]bool, len(pod.Spec.Volumes))
	for _, volume := range pod.Spec.Volumes {
		podVolumes[volume.Name] = true
	}

	for _, container := range injectedContainers {
		container.Env = append(container.Env, componentsEnvVars...)
		// mount volume as empty dir by default.
		patches = append(patches, emptyVolumePatches(container, podVolumes, pod)...)
		container.VolumeMounts = append(container.VolumeMounts, sharedSocketVolumeMount)

		patches = append(patches, patcher.PatchOperation{
			Op:    "add",
			Path:  patcher.PatchPathContainers + "/-",
			Value: container,
		})
	}

	return patches, &sharedSocketVolumeMount
}

// emptyVolumePatches return all patches for pod emptyvolumes (the default value for injected pluggable components)
func emptyVolumePatches(container corev1.Container, podVolumes map[string]bool, pod *corev1.Pod) []patcher.PatchOperation {
	volumePatches := make([]patcher.PatchOperation, 0)
	for _, volumeMount := range container.VolumeMounts {
		if !podVolumes[volumeMount.Name] {
			emptyDirVolume := corev1.Volume{
				Name: volumeMount.Name,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			}
			pod.Spec.Volumes = append(pod.Spec.Volumes, emptyDirVolume)
			volumePatches = append(volumePatches, patcher.PatchOperation{
				Op:    "add",
				Path:  patcher.PatchPathVolumes + "/-",
				Value: emptyDirVolume,
			})
		}
	}
	return volumePatches
}

// addSharedSocketVolume adds the new volume to the pod and return the patch operation and the mounted volume.
func addSharedSocketVolume(mountPath string, pod *corev1.Pod) (patcher.PatchOperation, corev1.VolumeMount) {
	sharedSocketVolume := sharedComponentsSocketVolume()
	sharedSocketVolumeMount := sharedComponentsUnixSocketVolumeMount(mountPath)

	var volumePatch patcher.PatchOperation

	if len(pod.Spec.Volumes) == 0 {
		volumePatch = patcher.PatchOperation{
			Op:    "add",
			Path:  patcher.PatchPathVolumes,
			Value: []corev1.Volume{sharedSocketVolume},
		}
	} else {
		volumePatch = patcher.PatchOperation{
			Op:    "add",
			Path:  patcher.PatchPathVolumes + "/-",
			Value: sharedSocketVolume,
		}
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, sharedSocketVolume)
	return volumePatch, sharedSocketVolumeMount
}
