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
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/kit/ptr"
)

// getVolumeMounts returns the list of VolumeMount's for the sidecar container.
func (c *SidecarConfig) getVolumeMounts() []corev1.VolumeMount {
	vs := append(
		parseVolumeMountsString(c.VolumeMounts, true),
		parseVolumeMountsString(c.VolumeMountsRW, false)...,
	)

	// Allocate with an extra 3 capacity because we are appending more volumes later
	volumeMounts := make([]corev1.VolumeMount, 0, len(vs)+3)
	for _, v := range vs {
		if podContainsVolume(c.pod, v.Name) {
			volumeMounts = append(volumeMounts, v)
		} else {
			log.Warnf("Volume %s is not present in pod %s, skipping", v.Name, c.pod.GetName())
		}
	}

	return volumeMounts
}

// getUnixDomainSocketVolumeMount returns a volume and mounts for the pod to append the UNIX domain socket.
func (c *SidecarConfig) getUnixDomainSocketVolumeMount() (vol corev1.Volume, daprdVolMount corev1.VolumeMount, appVolMount corev1.VolumeMount) {
	vol = corev1.Volume{
		Name: injectorConsts.UnixDomainSocketVolume,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium: corev1.StorageMediumMemory,
			},
		},
	}

	daprdVolMount = corev1.VolumeMount{
		Name:      injectorConsts.UnixDomainSocketVolume,
		MountPath: injectorConsts.UnixDomainSocketDaprdPath,
	}

	appVolMount = corev1.VolumeMount{
		Name:      injectorConsts.UnixDomainSocketVolume,
		MountPath: c.UnixDomainSocketPath,
	}

	return
}

// getTokenVolume returns the volume projection for the Kubernetes service account.
// Requests a new projected volume with a service account token for our specific audience.
func (c *SidecarConfig) getTokenVolume() corev1.Volume {
	return corev1.Volume{
		Name: injectorConsts.TokenVolumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: ptr.Of(int32(420)),
				Sources: []corev1.VolumeProjection{{
					ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
						Audience:          c.SentrySPIFFEID,
						ExpirationSeconds: ptr.Of(int64(7200)),
						Path:              "token",
					},
				}},
			},
		},
	}
}

func addVolumeMountToContainers(containers map[int]corev1.Container, addMounts corev1.VolumeMount) jsonpatch.Patch {
	volumeMount := []corev1.VolumeMount{addMounts}
	volumeMountPatchOps := make(jsonpatch.Patch, 0, len(containers))
	for i, container := range containers {
		patchOps := GetVolumeMountPatchOperations(container.VolumeMounts, volumeMount, i)
		volumeMountPatchOps = append(volumeMountPatchOps, patchOps...)
	}
	return volumeMountPatchOps
}

func (c *SidecarConfig) getVolumesPatchOperations(addVolumes []corev1.Volume, path string) jsonpatch.Patch {
	if len(c.pod.Spec.Volumes) == 0 {
		// If there are no volumes defined in the container, we initialize a slice of volumes.
		return jsonpatch.Patch{
			NewPatchOperation("add", path, addVolumes),
		}
	}

	// If there are existing volumes, then we are adding to an existing slice of volumes.
	path += "/-"

	patchOps := make(jsonpatch.Patch, len(addVolumes))
	n := 0
	for _, addVolume := range addVolumes {
		isConflict := false
		for _, mount := range c.pod.Spec.Volumes {
			// conflict cases
			if addVolume.Name == mount.Name {
				isConflict = true
				break
			}
		}

		if isConflict {
			continue
		}

		patchOps[n] = NewPatchOperation("add", path, addVolume)
		n++
	}

	return patchOps[:n]
}

func podContainsVolume(pod *corev1.Pod, name string) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}

// parseVolumeMountsString parses the annotation and returns volume mounts.
// The format of the annotation is: "mountPath1:hostPath1,mountPath2:hostPath2"
// The readOnly parameter applies to all mounts.
func parseVolumeMountsString(volumeMountStr string, readOnly bool) []corev1.VolumeMount {
	vs := strings.Split(volumeMountStr, ",")
	volumeMounts := make([]corev1.VolumeMount, 0, len(vs))
	for _, v := range vs {
		vmount := strings.Split(strings.TrimSpace(v), ":")
		if len(vmount) != 2 {
			continue
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      vmount[0],
			MountPath: vmount[1],
			ReadOnly:  readOnly,
		})
	}
	return volumeMounts
}
