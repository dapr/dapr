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
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/dapr/utils"
)

// splitContainers split containers between:
// - appContainers are containers related to apps.
// - componentContainers are containers related to pluggable components.
func (c *SidecarConfig) splitContainers() (appContainers map[int]corev1.Container, componentContainers map[int]corev1.Container) {
	appContainers = make(map[int]corev1.Container, len(c.pod.Spec.Containers))
	componentContainers = make(map[int]corev1.Container, len(c.pod.Spec.Containers))
	componentsNames := strings.Split(c.PluggableComponents, ",")
	isComponent := make(map[string]bool, len(componentsNames))
	for _, name := range componentsNames {
		isComponent[name] = true
	}

	for idx, container := range c.pod.Spec.Containers {
		if isComponent[container.Name] {
			componentContainers[idx] = container
		} else {
			appContainers[idx] = container
		}
	}

	return appContainers, componentContainers
}

// componentsPatchOps returns the patch operations required to properly bootstrap the pluggable component and the respective volume mount for the sidecar.
func (c *SidecarConfig) componentsPatchOps(componentContainers map[int]corev1.Container, injectedContainers []corev1.Container) (jsonpatch.Patch, *corev1.VolumeMount) {
	if len(componentContainers) == 0 && len(injectedContainers) == 0 {
		return jsonpatch.Patch{}, nil
	}

	patches := make(jsonpatch.Patch, 0, (len(injectedContainers)+len(componentContainers)+1)*2)

	mountPath := c.PluggableComponentsSocketsFolder
	if mountPath == "" {
		mountPath = utils.GetEnvOrElse(injectorConsts.ComponentsUDSMountPathEnvVar, injectorConsts.ComponentsUDSDefaultFolder)
	}

	sharedSocketVolume, sharedSocketVolumeMount, volumePatch := c.addSharedSocketVolume(mountPath)
	patches = append(patches, volumePatch)
	componentsEnvVars := []corev1.EnvVar{{
		Name:  injectorConsts.ComponentsUDSMountPathEnvVar,
		Value: sharedSocketVolumeMount.MountPath,
	}}

	for idx, container := range componentContainers {
		patches = append(patches, GetEnvPatchOperations(container.Env, componentsEnvVars, idx)...)
		patches = append(patches, GetVolumeMountPatchOperations(container.VolumeMounts, []corev1.VolumeMount{sharedSocketVolumeMount}, idx)...)
	}

	podVolumes := make(map[string]bool, len(c.pod.Spec.Volumes)+1)
	podVolumes[sharedSocketVolume.Name] = true
	for _, volume := range c.pod.Spec.Volumes {
		podVolumes[volume.Name] = true
	}

	for _, container := range injectedContainers {
		container.Env = append(container.Env, componentsEnvVars...)
		// mount volume as empty dir by default.
		_, patch := emptyVolumePatches(container, podVolumes)
		patches = append(patches, patch...)
		container.VolumeMounts = append(container.VolumeMounts, sharedSocketVolumeMount)

		patches = append(patches,
			NewPatchOperation("add", PatchPathContainers+"/-", container),
		)
	}

	return patches, &sharedSocketVolumeMount
}

// Injectable parses the container definition from components annotations returning them as a list. Uses the appID to filter
// only the eligble components for such apps avoiding injecting containers that will not be used.
func Injectable(appID string, components []componentsapi.Component) []corev1.Container {
	componentContainers := make([]corev1.Container, 0, len(components))
	componentImages := make(map[string]bool, len(components))

	for _, component := range components {
		containerAsStr := component.Annotations[annotations.KeyPluggableComponentContainer]
		if containerAsStr == "" {
			continue
		}
		var container *corev1.Container
		if err := json.Unmarshal([]byte(containerAsStr), &container); err != nil {
			log.Warnf("Could not unmarshal container %s: %v", component.Name, err)
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

// emptyVolumePatches return all patches for pod emptyvolumes (the default value for injected pluggable components) and the volumes.
func emptyVolumePatches(container corev1.Container, podVolumes map[string]bool) ([]corev1.Volume, jsonpatch.Patch) {
	volumes := make([]corev1.Volume, 0, len(container.VolumeMounts))
	volumePatches := make(jsonpatch.Patch, 0, len(container.VolumeMounts))
	for _, volumeMount := range container.VolumeMounts {
		if podVolumes[volumeMount.Name] {
			continue
		}

		emptyDirVolume := corev1.Volume{
			Name: volumeMount.Name,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}
		volumes = append(volumes, emptyDirVolume)
		volumePatches = append(volumePatches,
			NewPatchOperation("add", PatchPathVolumes+"/-", emptyDirVolume),
		)
	}
	return volumes, volumePatches
}

// addSharedSocketVolume adds the new volume to the pod and return the patch operation, the volume, and the volume mount.
func (c *SidecarConfig) addSharedSocketVolume(mountPath string) (corev1.Volume, corev1.VolumeMount, jsonpatch.Operation) {
	sharedSocketVolume := sharedComponentsSocketVolume()
	sharedSocketVolumeMount := sharedComponentsUnixSocketVolumeMount(mountPath)

	var volumePatch jsonpatch.Operation
	if len(c.pod.Spec.Volumes) == 0 {
		volumePatch = NewPatchOperation("add", PatchPathVolumes, []corev1.Volume{sharedSocketVolume})
	} else {
		volumePatch = NewPatchOperation("add", PatchPathVolumes+"/-", sharedSocketVolume)
	}

	return sharedSocketVolume, sharedSocketVolumeMount, volumePatch
}

// sharedComponentsSocketVolume creates a shared unix socket volume to be used by sidecar.
func sharedComponentsSocketVolume() corev1.Volume {
	return corev1.Volume{
		Name: injectorConsts.ComponentsUDSVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

// sharedComponentsUnixSocketVolumeMount creates a shared unix socket volume mount to be used by pluggable component.
func sharedComponentsUnixSocketVolumeMount(mountPath string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      injectorConsts.ComponentsUDSVolumeName,
		MountPath: mountPath,
	}
}
