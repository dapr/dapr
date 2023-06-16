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
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/patcher"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/kit/ptr"
)

// AddDaprEnvVarsToContainers adds Dapr environment variables to all the containers in any Dapr-enabled pod.
// The containers can be injected or user-defined.
func AddDaprEnvVarsToContainers(containers map[int]corev1.Container, appProtocol string) []patcher.PatchOperation {
	envPatchOps := make([]patcher.PatchOperation, 0, len(containers))
	envVars := []corev1.EnvVar{
		{
			Name:  UserContainerDaprHTTPPortName,
			Value: strconv.Itoa(SidecarHTTPPort),
		},
		{
			Name:  UserContainerDaprGRPCPortName,
			Value: strconv.Itoa(SidecarAPIGRPCPort),
		},
	}
	if appProtocol != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  UserContainerAppProtocolName,
			Value: appProtocol,
		})
	}
	for i, container := range containers {
		patchOps := patcher.GetEnvPatchOperations(container.Env, envVars, i)
		envPatchOps = append(envPatchOps, patchOps...)
	}
	return envPatchOps
}

// AddDaprSideCarInjectedLabel adds Dapr label to patch pod so list of patched pods can be retrieved more efficiently
func AddDaprSideCarInjectedLabel(labels map[string]string) patcher.PatchOperation {
	if len(labels) == 0 { // empty labels
		return patcher.PatchOperation{
			Op:   "add",
			Path: PatchPathLabels,
			Value: map[string]string{
				SidecarInjectedLabel: "true",
			},
		}
	}
	return patcher.PatchOperation{
		Op:    "add",
		Path:  PatchPathLabels + "/dapr.io~1sidecar-injected",
		Value: "true",
	}
}

// AddDaprSideCarAppIDLabel adds Dapr app-id label which can be handy for metric labels
func AddDaprSideCarAppIDLabel(appID string, labels map[string]string) patcher.PatchOperation {
	if len(labels) == 0 { // empty labels
		return patcher.PatchOperation{
			Op:   "add",
			Path: PatchPathLabels,
			Value: map[string]string{
				SidecarAppIDLabel: appID,
			},
		}
	}
	return patcher.PatchOperation{
		Op:    "add",
		Path:  PatchPathLabels + "/dapr.io~1app-id",
		Value: appID,
	}
}

// AddDaprSideCarMetricsEnabledLabel adds Dapr metrics-enabled label which can be handy for scraping metrics
func AddDaprSideCarMetricsEnabledLabel(metricsEnabled bool, labels map[string]string) patcher.PatchOperation {
	if len(labels) == 0 { // empty labels
		return patcher.PatchOperation{
			Op:   "add",
			Path: PatchPathLabels,
			Value: map[string]string{
				SidecarMetricsEnabledLabel: strconv.FormatBool(metricsEnabled),
			},
		}
	}
	return patcher.PatchOperation{
		Op:    "add",
		Path:  PatchPathLabels + "/dapr.io~1metrics-enabled",
		Value: strconv.FormatBool(metricsEnabled),
	}
}

// AddSocketVolumeMountToContainers adds the Dapr UNIX domain socket volume to all the containers in any Dapr-enabled pod.
func AddSocketVolumeMountToContainers(containers map[int]corev1.Container, socketVolumeMount *corev1.VolumeMount) []patcher.PatchOperation {
	if socketVolumeMount == nil {
		return []patcher.PatchOperation{}
	}

	return addVolumeMountToContainers(containers, *socketVolumeMount)
}

func addVolumeMountToContainers(containers map[int]corev1.Container, addMounts corev1.VolumeMount) []patcher.PatchOperation {
	volumeMount := []corev1.VolumeMount{addMounts}
	volumeMountPatchOps := make([]patcher.PatchOperation, 0, len(containers))
	for i, container := range containers {
		patchOps := patcher.GetVolumeMountPatchOperations(container.VolumeMounts, volumeMount, i)
		volumeMountPatchOps = append(volumeMountPatchOps, patchOps...)
	}
	return volumeMountPatchOps
}

func GetVolumesPatchOperations(volumes []corev1.Volume, addVolumes []corev1.Volume, path string) []patcher.PatchOperation {
	if len(volumes) == 0 {
		// If there are no volumes defined in the container, we initialize a slice of volumes.
		return []patcher.PatchOperation{
			{
				Op:    "add",
				Path:  path,
				Value: addVolumes,
			},
		}
	}

	// If there are existing volumes, then we are adding to an existing slice of volumes.
	path += "/-"

	patchOps := make([]patcher.PatchOperation, len(addVolumes))
	n := 0
	for _, addVolume := range addVolumes {
		isConflict := false
		for _, mount := range volumes {
			// conflict cases
			if addVolume.Name == mount.Name {
				isConflict = true
				break
			}
		}

		if isConflict {
			continue
		}

		patchOps[n] = patcher.PatchOperation{
			Op:    "add",
			Path:  path,
			Value: addVolume,
		}
		n++
	}

	return patchOps[:n]
}

// GetUnixDomainSocketVolumeMount returns a volume mount for the pod to append the UNIX domain socket.
func GetUnixDomainSocketVolumeMount(pod *corev1.Pod) *corev1.VolumeMount {
	unixDomainSocket := annotations.New(pod.Annotations).GetString(annotations.KeyUnixDomainSocketPath)
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

// GetTokenVolume returns the volume projection for the Kubernetes service account.
// Requests a new projected volume with a service account token for our specific audience.
func GetTokenVolume() corev1.Volume {
	return corev1.Volume{
		Name: TokenVolumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: ptr.Of(int32(420)),
				Sources: []corev1.VolumeProjection{{
					ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
						Audience:          sentryConsts.ServiceAccountTokenAudience,
						ExpirationSeconds: ptr.Of(int64(7200)),
						Path:              "token",
					},
				}},
			},
		},
	}
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

	an := annotations.New(pod.Annotations)
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
