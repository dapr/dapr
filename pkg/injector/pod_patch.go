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
	"strings"

	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/config/protocol"
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/components"
	"github.com/dapr/dapr/pkg/injector/patcher"
	"github.com/dapr/dapr/pkg/injector/sidecar"
	"github.com/dapr/dapr/pkg/validation"
)

const (
	defaultConfig      = "daprsystem"
	defaultMtlsEnabled = true
)

func (i *injector) getPodPatchOperations(ctx context.Context, ar *v1.AdmissionReview,
	namespace, image, imagePullPolicy string, kubeClient kubernetes.Interface, daprClient scheme.Interface,
) (patchOps []patcher.PatchOperation, err error) {
	req := ar.Request
	var pod corev1.Pod
	err = json.Unmarshal(req.Object.Raw, &pod)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal raw object: %w", err)
	}

	log.Infof(
		"AdmissionReview for Kind=%v, Namespace=%s Name=%s (%s) UID=%v patchOperation=%v UserInfo=%v",
		req.Kind,
		req.Namespace,
		req.Name,
		pod.Name,
		req.UID,
		req.Operation,
		req.UserInfo,
	)

	an := annotations.New(pod.Annotations)
	if !an.GetBoolOrDefault(annotations.KeyEnabled, false) || sidecar.PodContainsSidecarContainer(&pod) {
		return nil, nil
	}

	appID := sidecar.GetAppID(pod.ObjectMeta)
	metricsEnabled := sidecar.GetMetricsEnabled(pod.ObjectMeta)
	err = validation.ValidateKubernetesAppID(appID)
	if err != nil {
		return nil, err
	}

	// Keep DNS resolution outside of GetSidecarContainer for unit testing.
	placementAddress := sidecar.ServiceAddress(sidecar.ServicePlacement, namespace, i.config.KubeClusterDomain)
	sentryAddress := sidecar.ServiceAddress(sidecar.ServiceSentry, namespace, i.config.KubeClusterDomain)
	apiSvcAddress := sidecar.ServiceAddress(sidecar.ServiceAPI, namespace, i.config.KubeClusterDomain)

	trustAnchors, certChain, certKey := sidecar.GetTrustAnchorsAndCertChain(ctx, kubeClient, namespace)

	// Get all volume mounts
	volumeMounts := sidecar.GetVolumeMounts(pod)
	socketVolumeMount := sidecar.GetUnixDomainSocketVolumeMount(&pod)
	if socketVolumeMount != nil {
		volumeMounts = append(volumeMounts, *socketVolumeMount)
	}
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      sidecar.TokenVolumeName,
		MountPath: sidecar.TokenVolumeKubernetesMountPath,
		ReadOnly:  true,
	})

	// Pluggable components
	appContainers, componentContainers, injectedComponentContainers, err := i.splitContainers(pod)
	if err != nil {
		return nil, err
	}
	componentPatchOps, componentsSocketVolumeMount := components.PatchOps(componentContainers, injectedComponentContainers, &pod)

	// Projected volume with the token
	tokenVolume := sidecar.GetTokenVolume()

	// Get the sidecar container
	sidecarContainer, err := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
		AppID:                        appID,
		Annotations:                  an,
		CertChain:                    certChain,
		CertKey:                      certKey,
		ControlPlaneAddress:          apiSvcAddress,
		DaprSidecarImage:             image,
		Identity:                     req.Namespace + ":" + pod.Spec.ServiceAccountName,
		IgnoreEntrypointTolerations:  i.config.GetIgnoreEntrypointTolerations(),
		ImagePullPolicy:              i.config.GetPullPolicy(),
		MTLSEnabled:                  mTLSEnabled(daprClient),
		Namespace:                    req.Namespace,
		PlacementServiceAddress:      placementAddress,
		SentryAddress:                sentryAddress,
		Tolerations:                  pod.Spec.Tolerations,
		TrustAnchors:                 trustAnchors,
		VolumeMounts:                 volumeMounts,
		ComponentsSocketsVolumeMount: componentsSocketVolumeMount,
		SkipPlacement:                i.config.GetSkipPlacement(),
		RunAsNonRoot:                 i.config.GetRunAsNonRoot(),
		ReadOnlyRootFilesystem:       i.config.GetReadOnlyRootFilesystem(),
		SidecarDropALLCapabilities:   i.config.GetDropCapabilities(),
	})
	if err != nil {
		return nil, err
	}

	// Create the list of patch operations
	patchOps = []patcher.PatchOperation{}
	if len(pod.Spec.Containers) == 0 { // set to empty to support add operations individually
		patchOps = append(patchOps, patcher.PatchOperation{
			Op:    "add",
			Path:  patcher.PatchPathContainers,
			Value: []corev1.Container{},
		})
	}

	patchOps = append(patchOps,
		patcher.PatchOperation{
			Op:    "add",
			Path:  patcher.PatchPathContainers + "/-",
			Value: sidecarContainer,
		},
		sidecar.AddDaprSideCarInjectedLabel(pod.Labels),
		sidecar.AddDaprSideCarAppIDLabel(appID, pod.Labels),
		sidecar.AddDaprSideCarMetricsEnabledLabel(metricsEnabled, pod.Labels))

	patchOps = append(patchOps,
		sidecar.AddDaprEnvVarsToContainers(appContainers, getAppProtocol(an))...)
	patchOps = append(patchOps,
		sidecar.AddSocketVolumeMountToContainers(appContainers, socketVolumeMount)...)
	volumePatchOps := sidecar.GetVolumesPatchOperations(
		pod.Spec.Volumes,
		[]corev1.Volume{tokenVolume},
		patcher.PatchPathVolumes,
	)
	patchOps = append(patchOps, volumePatchOps...)
	patchOps = append(patchOps, componentPatchOps...)

	return patchOps, nil
}

func mTLSEnabled(daprClient scheme.Interface) bool {
	resp, err := daprClient.ConfigurationV1alpha1().Configurations(metaV1.NamespaceAll).List(metaV1.ListOptions{})
	if err != nil {
		log.Errorf("Failed to load dapr configuration from k8s, use default value %t for mTLSEnabled: %s", defaultMtlsEnabled, err)
		return defaultMtlsEnabled
	}

	for _, c := range resp.Items {
		if c.GetName() == defaultConfig {
			return c.Spec.MTLSSpec.GetEnabled()
		}
	}
	log.Infof("Dapr system configuration (%s) is not found, use default value %t for mTLSEnabled", defaultConfig, defaultMtlsEnabled)
	return defaultMtlsEnabled
}

func getAppProtocol(an annotations.Map) string {
	appProtocol := strings.ToLower(an.GetString(annotations.KeyAppProtocol))
	appSSL := an.GetBoolOrDefault(annotations.KeyAppSSL, annotations.DefaultAppSSL)

	switch appProtocol {
	case string(protocol.GRPCSProtocol), string(protocol.HTTPSProtocol), string(protocol.H2CProtocol):
		return appProtocol
	case string(protocol.HTTPProtocol):
		// For backwards compatibility, when protocol is HTTP and --app-ssl is set, use "https"
		// TODO: Remove in a future Dapr version
		if appSSL {
			return string(protocol.HTTPSProtocol)
		} else {
			return string(protocol.HTTPProtocol)
		}
	case string(protocol.GRPCProtocol):
		// For backwards compatibility, when protocol is GRPC and --app-ssl is set, use "grpcs"
		// TODO: Remove in a future Dapr version
		if appSSL {
			return string(protocol.GRPCSProtocol)
		} else {
			return string(protocol.GRPCProtocol)
		}
	case "":
		return string(protocol.HTTPProtocol)
	default:
		return ""
	}
}
