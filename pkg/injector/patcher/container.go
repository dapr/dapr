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
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// GetEnvPatchOperations adds new environment variables only if they do not exist.
// It does not override existing values for those variables if they have been defined already.
func GetEnvPatchOperations(envs []corev1.EnvVar, addEnv []corev1.EnvVar, containerIdx int) []PatchOperation {
	path := fmt.Sprintf("%s/%d/env", PatchPathContainers, containerIdx)
	if len(envs) == 0 {
		// If there are no environment variables defined in the container, we initialize a slice of environment vars.
		return []PatchOperation{
			{
				Op:    "add",
				Path:  path,
				Value: addEnv,
			},
		}
	}

	// If there are existing env vars, then we are adding to an existing slice of env vars.
	path += "/-"

	// Get a map with all the existing env var names
	existing := make(map[string]struct{}, len(envs))
	for _, e := range envs {
		existing[e.Name] = struct{}{}
	}

	patchOps := make([]PatchOperation, len(addEnv))
	n := 0
	for _, env := range addEnv {
		// Add only env vars that do not conflict with existing user defined/injected env vars.
		_, ok := existing[env.Name]
		if ok {
			continue
		}

		patchOps[n] = PatchOperation{
			Op:    "add",
			Path:  path,
			Value: env,
		}
		n++
	}
	return patchOps[:n]
}

// GetVolumeMountPatchOperations gets the patch operations for volume mounts
func GetVolumeMountPatchOperations(volumeMounts []corev1.VolumeMount, addMounts []corev1.VolumeMount, containerIdx int) []PatchOperation {
	path := fmt.Sprintf("%s/%d/volumeMounts", PatchPathContainers, containerIdx)
	if len(volumeMounts) == 0 {
		// If there are no volume mounts defined in the container, we initialize a slice of volume mounts.
		return []PatchOperation{
			{
				Op:    "add",
				Path:  path,
				Value: addMounts,
			},
		}
	}

	// If there are existing volume mounts, then we are adding to an existing slice of volume mounts.
	path += "/-"

	// Get a map with all the existingMounts mount paths
	existingMounts := make(map[string]struct{}, len(volumeMounts))
	existingNames := make(map[string]struct{}, len(volumeMounts))
	for _, m := range volumeMounts {
		existingMounts[m.MountPath] = struct{}{}
		existingNames[m.Name] = struct{}{}
	}

	patchOps := make([]PatchOperation, len(addMounts))
	n := 0
	var ok bool
	for _, addMount := range addMounts {
		// Do not add the mount if a volume is already mounted on the same path or has the same name
		if _, ok = existingMounts[addMount.MountPath]; ok {
			continue
		}
		if _, ok = existingNames[addMount.Name]; ok {
			continue
		}

		patchOps[n] = PatchOperation{
			Op:    "add",
			Path:  path,
			Value: addMount,
		}
		n++
	}

	return patchOps[:n]
}
