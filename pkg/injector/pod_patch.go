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

package injector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/dapr/pkg/validation"
)

const (
	apiService           = "dapr-api"
	apiServicePort       = 80
	placementService     = "dapr-placement-server"
	placementServicePort = 50005
	sentryService        = "dapr-sentry"
	sentryServicePort    = 80
	kubernetesMountPath  = "/var/run/secrets/kubernetes.io/serviceaccount"
	defaultConfig        = "daprsystem"
	defaultMtlsEnabled   = true
)

func (i *injector) getPodPatchOperations(ar *v1.AdmissionReview,
	namespace, image, imagePullPolicy string, kubeClient kubernetes.Interface, daprClient scheme.Interface,
) ([]sidecar.PatchOperation, error) {
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

	an := sidecar.Annotations(pod.Annotations)
	if !an.GetBoolOrDefault(annotations.KeyEnabled, false) || sidecar.PodContainsSidecarContainer(&pod) {
		return nil, nil
	}

	appID := sidecar.GetAppID(pod.ObjectMeta)
	if err := validation.ValidateKubernetesAppID(appID); err != nil {
		return nil, err
	}

	// Keep DNS resolution outside of GetSidecarContainer for unit testing.
	placementAddress := getServiceAddress(placementService, namespace, i.config.KubeClusterDomain, placementServicePort)
	sentryAddress := getServiceAddress(sentryService, namespace, i.config.KubeClusterDomain, sentryServicePort)
	apiSvcAddress := getServiceAddress(apiService, namespace, i.config.KubeClusterDomain, apiServicePort)

	trustAnchors, certChain, certKey := getTrustAnchorsAndCertChain(kubeClient, namespace)
	socketVolumeMount := sidecar.GetUnixDomainSocketVolume(&pod)

	sidecarContainer, err := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
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
		envPatchOps          []sidecar.PatchOperation
		socketVolumePatchOps []sidecar.PatchOperation
	)
	if len(pod.Spec.Containers) == 0 {
		path = sidecar.ContainersPath
		value = []corev1.Container{*sidecarContainer}
	} else {
		envPatchOps = sidecar.AddDaprEnvVarsToContainers(pod.Spec.Containers)
		socketVolumePatchOps = sidecar.AddSocketVolumeToContainers(pod.Spec.Containers, socketVolumeMount)
		path = "/spec/containers/-"
		value = sidecarContainer
	}

	patchOps := []sidecar.PatchOperation{
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

func getServiceAddress(name, namespace, clusterDomain string, port int) string {
	return fmt.Sprintf("%s.%s.svc.%s:%d", name, namespace, clusterDomain, port)
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

	an := sidecar.Annotations(pod.Annotations)
	vs := append(
		sidecar.ParseVolumeMountsString(an.GetString(annotations.KeyVolumeMountsReadOnly), true),
		sidecar.ParseVolumeMountsString(an.GetString(annotations.KeyVolumeMountsReadWrite), false)...,
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
