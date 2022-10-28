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

package sidecar

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/injector/annotations"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
)

// PatchOperation represents a discreet change to be applied to a Kubernetes resource.
type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// DaprPortEnv contains the env vars that are set in containers to pass the ports used by Dapr.
var DaprPortEnv = []corev1.EnvVar{
	{
		Name:  UserContainerDaprHTTPPortName,
		Value: strconv.Itoa(SidecarHTTPPort),
	},
	{
		Name:  UserContainerDaprGRPCPortName,
		Value: strconv.Itoa(SidecarAPIGRPCPort),
	},
}

// AddDaprEnvVarsToContainers adds Dapr environment variables to all the containers in any Dapr-enabled pod.
// The containers can be injected or user-defined.
func AddDaprEnvVarsToContainers(containers map[int]corev1.Container) []PatchOperation {
	envPatchOps := make([]PatchOperation, 0, len(containers))
	for i, container := range containers {
		patchOps := GetEnvPatchOperations(container.Env, DaprPortEnv, i)
		envPatchOps = append(envPatchOps, patchOps...)
	}
	return envPatchOps
}

// GetEnvPatchOperations adds new environment variables only if they do not exist.
// It does not override existing values for those variables if they have been defined already.
func GetEnvPatchOperations(envs []corev1.EnvVar, addEnv []corev1.EnvVar, containerIdx int) []PatchOperation {
	path := fmt.Sprintf("%s/%d/env", ContainersPath, containerIdx)
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

	var patchOps []PatchOperation
LoopEnv:
	for _, env := range addEnv {
		for _, actual := range envs {
			if actual.Name == env.Name {
				// Add only env vars that do not conflict with existing user defined/injected env vars.
				continue LoopEnv
			}
		}
		patchOps = append(patchOps, PatchOperation{
			Op:    "add",
			Path:  path,
			Value: env,
		})
	}
	return patchOps
}

// AddSocketVolumeToContainers adds the Dapr UNIX domain socket volume to all the containers in any Dapr-enabled pod.
func AddSocketVolumeToContainers(containers map[int]corev1.Container, socketVolumeMount *corev1.VolumeMount) []PatchOperation {
	if socketVolumeMount == nil {
		return []PatchOperation{}
	}

	return addVolumeToContainers(containers, *socketVolumeMount)
}

func addVolumeToContainers(containers map[int]corev1.Container, addMounts corev1.VolumeMount) []PatchOperation {
	volumeMount := []corev1.VolumeMount{addMounts}
	volumeMountPatchOps := make([]PatchOperation, 0, len(containers))
	for i, container := range containers {
		patchOps := GetVolumeMountPatchOperations(container.VolumeMounts, volumeMount, i)
		volumeMountPatchOps = append(volumeMountPatchOps, patchOps...)
	}
	return volumeMountPatchOps
}

// It does not override existing values for those variables if they have been defined already.
func GetVolumeMountPatchOperations(volumeMounts []corev1.VolumeMount, addMounts []corev1.VolumeMount, containerIdx int) []PatchOperation {
	path := fmt.Sprintf("%s/%d/volumeMounts", ContainersPath, containerIdx)
	if len(volumeMounts) == 0 {
		// If there are no volume mount variables defined in the container, we initialize a slice of environment vars.
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

	var patchOps []PatchOperation

	for _, addMount := range addMounts {
		isConflict := false
		for _, mount := range volumeMounts {
			// conflict cases
			if addMount.Name == mount.Name || addMount.MountPath == mount.MountPath {
				isConflict = true
				break
			}
		}

		if !isConflict {
			patchOps = append(patchOps, PatchOperation{
				Op:    "add",
				Path:  path,
				Value: addMount,
			})
		}
	}

	return patchOps
}

// GetUnixDomainSocketVolume returns a volume mount for the pod to append the UNIX domain socket.
func GetUnixDomainSocketVolume(pod *corev1.Pod) *corev1.VolumeMount {
	unixDomainSocket := Annotations(pod.Annotations).GetString(annotations.KeyUnixDomainSocketPath)
	if unixDomainSocket == "" {
		return nil
	}

	// socketVolume is an EmptyDir
	socketVolume := &corev1.Volume{
		Name: UnixDomainSocketVolume,
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, *socketVolume)

	return &corev1.VolumeMount{Name: UnixDomainSocketVolume, MountPath: unixDomainSocket}
}

// GetTokenVolumeMount returns the VolumeMount for the Kubernetes service account.
func GetTokenVolumeMount(podSpec corev1.PodSpec) *corev1.VolumeMount {
	for _, c := range podSpec.Containers {
		for _, v := range c.VolumeMounts {
			if v.MountPath == KubernetesMountPath {
				return &v
			}
		}
	}
	return nil
}

// GetTrustAnchorsAndCertChain returns the trust anchor and certs.
func GetTrustAnchorsAndCertChain(ctx context.Context, kubeClient kubernetes.Interface, namespace string) (string, string, string) {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(ctx, sentryConsts.TrustBundleK8sSecretName, metav1.GetOptions{})
	if err != nil {
		return "", "", ""
	}

	rootCert := secret.Data[credentials.RootCertFilename]
	certChain := secret.Data[credentials.IssuerCertFilename]
	certKey := secret.Data[credentials.IssuerKeyFilename]
	return string(rootCert), string(certChain), string(certKey)
}

// GetVolumeMounts returns the list of VolumeMount's for the sidecar container.
func GetVolumeMounts(pod corev1.Pod) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{}

	an := Annotations(pod.Annotations)
	vs := append(
		ParseVolumeMountsString(an.GetString(annotations.KeyVolumeMountsReadOnly), true),
		ParseVolumeMountsString(an.GetString(annotations.KeyVolumeMountsReadWrite), false)...,
	)

	for _, v := range vs {
		if podContainsVolume(pod, v.Name) {
			volumeMounts = append(volumeMounts, v)
		} else {
			log.Warnf("volume %s is not present in pod %s, skipping.", v.Name, pod.Name)
		}
	}

	return volumeMounts
}

func podContainsVolume(pod corev1.Pod, name string) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}
