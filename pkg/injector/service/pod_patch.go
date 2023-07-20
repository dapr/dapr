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

package service

import (
	"context"
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/credentials"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/dapr/pkg/injector/patcher"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
)

const (
	defaultConfig      = "daprsystem"
	defaultMtlsEnabled = true
)

func (i *injector) getPodPatchOperations(ctx context.Context, ar *admissionv1.AdmissionReview) (patchOps jsonpatch.Patch, err error) {
	pod := &corev1.Pod{}
	err = json.Unmarshal(ar.Request.Object.Raw, pod)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal raw object: %w", err)
	}

	log.Infof(
		"AdmissionReview for Kind=%v, Namespace=%s Name=%s (%s) UID=%v patchOperation=%v UserInfo=%v",
		ar.Request.Kind, ar.Request.Namespace, ar.Request.Name, pod.Name, ar.Request.UID, ar.Request.Operation, ar.Request.UserInfo,
	)

	// Keep DNS resolution outside of GetSidecarContainer for unit testing.
	placementAddress := patcher.ServiceAddress(patcher.ServicePlacement, i.config.Namespace, i.config.KubeClusterDomain)
	sentryAddress := patcher.ServiceAddress(patcher.ServiceSentry, i.config.Namespace, i.config.KubeClusterDomain)
	operatorAddress := patcher.ServiceAddress(patcher.ServiceAPI, i.config.Namespace, i.config.KubeClusterDomain)

	// Get the TLS credentials
	trustAnchors, certChain, certKey := GetTrustAnchorsAndCertChain(ctx, i.kubeClient, i.config.Namespace)

	// Create the sidecar configuration object from the pod
	sidecar := patcher.NewSidecarConfig(pod)
	sidecar.GetInjectedComponentContainers = i.getInjectedComponentContainers
	sidecar.Mode = injectorConsts.ModeKubernetes
	sidecar.Namespace = ar.Request.Namespace
	sidecar.TrustAnchors = trustAnchors
	sidecar.CertChain = certChain
	sidecar.CertKey = certKey
	sidecar.MTLSEnabled = mTLSEnabled(i.daprClient)
	sidecar.Identity = ar.Request.Namespace + ":" + pod.Spec.ServiceAccountName
	sidecar.IgnoreEntrypointTolerations = i.config.GetIgnoreEntrypointTolerations()
	sidecar.ImagePullPolicy = i.config.GetPullPolicy()
	sidecar.OperatorAddress = operatorAddress
	sidecar.SentryAddress = sentryAddress
	sidecar.RunAsNonRoot = i.config.GetRunAsNonRoot()
	sidecar.ReadOnlyRootFilesystem = i.config.GetReadOnlyRootFilesystem()
	sidecar.SidecarDropALLCapabilities = i.config.GetDropCapabilities()

	// Set the placement address unless it's skipped
	// Even if the placement is skipped, however,the placement address will still be included if explicitly set in the annotations
	// We still include PlacementServiceAddress if explicitly set as annotation
	if !i.config.GetSkipPlacement() {
		sidecar.PlacementAddress = placementAddress
	}

	// Default value for the sidecar image, which can be overridden by annotations
	sidecar.SidecarImage = i.config.SidecarImage

	// Set the configuration from annotations
	sidecar.SetFromPodAnnotations()

	// Get the patch to apply to the pod
	// Patch may be empty if there's nothing that needs to be done
	patch, err := sidecar.GetPatch()
	if err != nil {
		return nil, err
	}
	return patch, nil
}

func mTLSEnabled(daprClient scheme.Interface) bool {
	resp, err := daprClient.ConfigurationV1alpha1().
		Configurations(metav1.NamespaceAll).
		List(metav1.ListOptions{})
	if err != nil {
		log.Errorf("Failed to load dapr configuration from k8s, use default value %t for mTLSEnabled: %s", defaultMtlsEnabled, err)
		return defaultMtlsEnabled
	}

	for _, c := range resp.Items {
		if c.GetName() == defaultConfig {
			return c.Spec.MTLSSpec.GetEnabled()
		}
	}
	log.Infof("Dapr system configuration '%s' does not exist; using default value %t for mTLSEnabled", defaultConfig, defaultMtlsEnabled)
	return defaultMtlsEnabled
}

// GetTrustAnchorsAndCertChain returns the trust anchor and certs.
func GetTrustAnchorsAndCertChain(ctx context.Context, kubeClient kubernetes.Interface, namespace string) (string, string, string) {
	secret, err := kubeClient.CoreV1().
		Secrets(namespace).
		Get(ctx, sentryConsts.TrustBundleK8sSecretName, metav1.GetOptions{})
	if err != nil {
		return "", "", ""
	}

	rootCert := secret.Data[credentials.RootCertFilename]
	certChain := secret.Data[credentials.IssuerCertFilename]
	certKey := secret.Data[credentials.IssuerKeyFilename]
	return string(rootCert), string(certChain), string(certKey)
}
