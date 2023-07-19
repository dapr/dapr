package injector

import (
	"strings"

	"github.com/dapr/dapr/pkg/components/pluggable"
	"github.com/dapr/dapr/pkg/injector/patcher"
	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"
)

// SplitContainers split containers between:
// - appContainers are containers related to apps.
// - componentContainers are containers related to pluggable components.
func (c *SidecarConfig) SplitContainers() (appContainers map[int]corev1.Container, componentContainers map[int]corev1.Container) {
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

// ComponentsPatchOps returns the patch operations required to properly bootstrap the pluggable component and the respective volume mount for the sidecar.
func (c *SidecarConfig) ComponentsPatchOps(componentContainers map[int]corev1.Container, injectedContainers []corev1.Container) (jsonpatch.Patch, *corev1.VolumeMount) {
	if len(componentContainers) == 0 && len(injectedContainers) == 0 {
		return jsonpatch.Patch{}, nil
	}

	patches := make(jsonpatch.Patch, 0, (len(injectedContainers)+len(componentContainers)+1)*2)

	mountPath := c.PluggableComponentsSocketsFolder
	if mountPath == "" {
		mountPath = pluggable.GetSocketFolderPath()
	}

	sharedSocketVolume, sharedSocketVolumeMount, volumePatch := c.addSharedSocketVolume(mountPath)
	patches = append(patches, volumePatch)
	componentsEnvVars := []corev1.EnvVar{{
		Name:  ComponentsUDSMountPathEnvVar,
		Value: sharedSocketVolumeMount.MountPath,
	}}

	for idx, container := range componentContainers {
		patches = append(patches, patcher.GetEnvPatchOperations(container.Env, componentsEnvVars, idx)...)
		patches = append(patches, patcher.GetVolumeMountPatchOperations(container.VolumeMounts, []corev1.VolumeMount{sharedSocketVolumeMount}, idx)...)
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
			patcher.NewPatchOperation("add", patcher.PatchPathContainers+"/-", container),
		)
	}

	return patches, &sharedSocketVolumeMount
}

// emptyVolumePatches return all patches for pod emptyvolumes (the default value for injected pluggable components)
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
			patcher.NewPatchOperation("add", patcher.PatchPathVolumes+"/-", emptyDirVolume),
		)
	}
	return volumes, volumePatches
}

// addSharedSocketVolume adds the new volume to the pod and return the patch operation and the mounted volume.
func (c *SidecarConfig) addSharedSocketVolume(mountPath string) (corev1.Volume, corev1.VolumeMount, jsonpatch.Operation) {
	sharedSocketVolume := sharedComponentsSocketVolume()
	sharedSocketVolumeMount := sharedComponentsUnixSocketVolumeMount(mountPath)

	var volumePatch jsonpatch.Operation
	if len(c.pod.Spec.Volumes) == 0 {
		volumePatch = patcher.NewPatchOperation("add", patcher.PatchPathVolumes, []corev1.Volume{sharedSocketVolume})
	} else {
		volumePatch = patcher.NewPatchOperation("add", patcher.PatchPathVolumes+"/-", sharedSocketVolume)
	}

	return sharedSocketVolume, sharedSocketVolumeMount, volumePatch
}

// sharedComponentsSocketVolume creates a shared unix socket volume to be used by sidecar.
func sharedComponentsSocketVolume() corev1.Volume {
	return corev1.Volume{
		Name: ComponentsUDSVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

// sharedComponentsUnixSocketVolumeMount creates a shared unix socket volume mount to be used by pluggable component.
func sharedComponentsUnixSocketVolumeMount(mountPath string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      ComponentsUDSVolumeName,
		MountPath: mountPath,
	}
}
