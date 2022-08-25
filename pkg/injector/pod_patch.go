/*
Copyright 2021 The Dapr Authors
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

package injector

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecarcontainer"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/dapr/pkg/validation"
)

//nolint:gosec
const (
	containersPath                = "/spec/containers"
	sidecarHTTPPort               = 3500
	sidecarAPIGRPCPort            = 50001
	sidecarInternalGRPCPort       = 50002
	sidecarPublicPort             = 3501
	userContainerDaprHTTPPortName = "DAPR_HTTP_PORT"
	userContainerDaprGRPCPortName = "DAPR_GRPC_PORT"
	apiAddress                    = "dapr-api"
	placementService              = "dapr-placement-server"
	sentryService                 = "dapr-sentry"
	apiPort                       = 80
	placementServicePort          = 50005
	sentryServicePort             = 80
	kubernetesMountPath           = "/var/run/secrets/kubernetes.io/serviceaccount"
	defaultConfig                 = "daprsystem"
	defaultSidecarListenAddresses = "[::1],127.0.0.1"
	defaultMtlsEnabled            = true
)

func (i *injector) getPodPatchOperations(ar *v1.AdmissionReview,
	namespace, image, imagePullPolicy string, kubeClient kubernetes.Interface, daprClient scheme.Interface,
) ([]PatchOperation, error) {
	req := ar.Request
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		errors.Wrap(err, "could not unmarshal raw object")
		return nil, err
	}

	log.Infof(
		"AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v "+
			"patchOperation=%v UserInfo=%v",
		req.Kind,
		req.Namespace,
		req.Name,
		pod.Name,
		req.UID,
		req.Operation,
		req.UserInfo,
	)

	an := sidecarcontainer.Annotations(pod.Annotations)
	if !an.GetBoolOrDefault(annotations.KeyEnabled, false) || podContainsSidecarContainer(&pod) {
		return nil, nil
	}

	appID := getAppID(pod)
	if err := validation.ValidateKubernetesAppID(appID); err != nil {
		return nil, err
	}

	// Keep DNS resolution outside of getSidecarContainer for unit testing.
	placementAddress := getServiceAddress(placementService, namespace, i.config.KubeClusterDomain, placementServicePort)
	sentryAddress := getServiceAddress(sentryService, namespace, i.config.KubeClusterDomain, sentryServicePort)
	apiSvcAddress := getServiceAddress(apiAddress, namespace, i.config.KubeClusterDomain, apiPort)

	trustAnchors, certChain, certKey := getTrustAnchorsAndCertChain(kubeClient, namespace)
	socketVolumeMount := appendUnixDomainSocketVolume(&pod)

	sidecarContainer, err := sidecarcontainer.GetSidecarContainer(sidecarcontainer.ContainerConfig{
		AppID:                       appID,
		Annotations:                 an,
		CertChain:                   certChain,
		CertKey:                     certKey,
		ControlPlaneAddress:         apiSvcAddress,
		DaprSidecarImage:            image,
		Identity:                    fmt.Sprintf("%s:%s", req.Namespace, pod.Spec.ServiceAccountName),
		IgnoreEntrypointTolerations: i.config.GetIgnoreEntrypointTolerations(),
		ImagePullPolicy:             i.config.GetPullPolicy(),
		MTLSEnabled:                 mTLSEnabled(daprClient),
		Namespace:                   req.Namespace,
		PlacementServiceAddress:     placementAddress,
		SentryAddress:               sentryAddress,
		SocketVolumeMount:           socketVolumeMount,
		TokenVolumeMount:            getTokenVolumeMount(pod),
		Tolerations:                 pod.Spec.Tolerations,
		TrustAnchors:                trustAnchors,
		VolumeMounts:                getVolumeMounts(pod),
	})
	if err != nil {
		return nil, err
	}

	var (
		path                 string
		value                any
		envPatchOps          []PatchOperation
		socketVolumePatchOps []PatchOperation
	)
	if len(pod.Spec.Containers) == 0 {
		path = containersPath
		value = []corev1.Container{*sidecarContainer}
	} else {
		envPatchOps = addDaprEnvVarsToContainers(pod.Spec.Containers)
		socketVolumePatchOps = addSocketVolumeToContainers(pod.Spec.Containers, socketVolumeMount)
		path = "/spec/containers/-"
		value = sidecarContainer
	}

	patchOps := []PatchOperation{
		{
			Op:    "add",
			Path:  path,
			Value: value,
		},
	}
	patchOps = append(patchOps, envPatchOps...)
	patchOps = append(patchOps, socketVolumePatchOps...)

	return patchOps, nil
}

// This function add Dapr environment variables to all the containers in any Dapr enabled pod.
// The containers can be injected or user defined.
func addDaprEnvVarsToContainers(containers []corev1.Container) []PatchOperation {
	portEnv := []corev1.EnvVar{
		{
			Name:  userContainerDaprHTTPPortName,
			Value: strconv.Itoa(sidecarHTTPPort),
		},
		{
			Name:  userContainerDaprGRPCPortName,
			Value: strconv.Itoa(sidecarAPIGRPCPort),
		},
	}
	envPatchOps := make([]PatchOperation, 0, len(containers))
	for i, container := range containers {
		path := fmt.Sprintf("%s/%d/env", containersPath, i)
		patchOps := getEnvPatchOperations(container.Env, portEnv, path)
		envPatchOps = append(envPatchOps, patchOps...)
	}
	return envPatchOps
}

// This function only add new environment variables if they do not exist.
// It does not override existing values for those variables if they have been defined already.
func getEnvPatchOperations(envs []corev1.EnvVar, addEnv []corev1.EnvVar, path string) []PatchOperation {
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

// This function add Dapr unix domain socket volume to all the containers in any Dapr enabled pod.
func addSocketVolumeToContainers(containers []corev1.Container, socketVolumeMount *corev1.VolumeMount) []PatchOperation {
	if socketVolumeMount == nil {
		return []PatchOperation{}
	}

	return addVolumeToContainers(containers, *socketVolumeMount)
}

func addVolumeToContainers(containers []corev1.Container, addMounts corev1.VolumeMount) []PatchOperation {
	volumeMount := []corev1.VolumeMount{addMounts}
	volumeMountPatchOps := make([]PatchOperation, 0, len(containers))
	for i, container := range containers {
		path := fmt.Sprintf("%s/%d/volumeMounts", containersPath, i)
		patchOps := getVolumeMountPatchOperations(container.VolumeMounts, volumeMount, path)
		volumeMountPatchOps = append(volumeMountPatchOps, patchOps...)
	}
	return volumeMountPatchOps
}

// It does not override existing values for those variables if they have been defined already.
func getVolumeMountPatchOperations(volumeMounts []corev1.VolumeMount, addMounts []corev1.VolumeMount, path string) []PatchOperation {
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

func getTrustAnchorsAndCertChain(kubeClient kubernetes.Interface, namespace string) (string, string, string) {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), sentryConsts.KubeScrtName, metaV1.GetOptions{})
	if err != nil {
		return "", "", ""
	}
	rootCert := secret.Data[credentials.RootCertFilename]
	certChain := secret.Data[credentials.IssuerCertFilename]
	certKey := secret.Data[credentials.IssuerKeyFilename]
	return string(rootCert), string(certChain), string(certKey)
}

func mTLSEnabled(daprClient scheme.Interface) bool {
	resp, err := daprClient.ConfigurationV1alpha1().Configurations(metaV1.NamespaceAll).List(metaV1.ListOptions{})
	if err != nil {
		log.Errorf("Failed to load dapr configuration from k8s, use default value %t for mTLSEnabled: %s", defaultMtlsEnabled, err)
		return defaultMtlsEnabled
	}

	for _, c := range resp.Items {
		if c.GetName() == defaultConfig {
			return c.Spec.MTLSSpec.Enabled
		}
	}
	log.Infof("Dapr system configuration (%s) is not found, use default value %t for mTLSEnabled", defaultConfig, defaultMtlsEnabled)
	return defaultMtlsEnabled
}

func getTokenVolumeMount(pod corev1.Pod) *corev1.VolumeMount {
	for _, c := range pod.Spec.Containers {
		for _, v := range c.VolumeMounts {
			if v.MountPath == kubernetesMountPath {
				return &v
			}
		}
	}
	return nil
}

func podContainsSidecarContainer(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == sidecarcontainer.SidecarContainerName {
			return true
		}
	}
	return false
}

func getServiceAddress(name, namespace, clusterDomain string, port int) string {
	return fmt.Sprintf("%s.%s.svc.%s:%d", name, namespace, clusterDomain, port)
}

func appendUnixDomainSocketVolume(pod *corev1.Pod) *corev1.VolumeMount {
	unixDomainSocket := sidecarcontainer.Annotations(pod.Annotations).GetString(annotations.KeyUnixDomainSocketPath)
	if unixDomainSocket == "" {
		return nil
	}

	// socketVolume is an EmptyDir
	socketVolume := &corev1.Volume{
		Name: sidecarcontainer.UnixDomainSocketVolume,
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, *socketVolume)

	return &corev1.VolumeMount{Name: sidecarcontainer.UnixDomainSocketVolume, MountPath: unixDomainSocket}
}

func podContainsVolume(pod corev1.Pod, name string) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}

func getVolumeMounts(pod corev1.Pod) []corev1.VolumeMount {
	volumeMounts := []corev1.VolumeMount{}

	an := sidecarcontainer.Annotations(pod.Annotations)
	vs := append(
		sidecarcontainer.ParseVolumeMountsString(an.GetString(annotations.KeyVolumeMountsReadOnly), true),
		sidecarcontainer.ParseVolumeMountsString(an.GetString(annotations.KeyVolumeMountsReadWrite), false)...,
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

func getAppID(pod corev1.Pod) string {
	return sidecarcontainer.Annotations(pod.Annotations).GetStringOrDefault(annotations.KeyAppID, pod.GetName())
}
