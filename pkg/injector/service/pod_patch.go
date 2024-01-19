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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/dapr/pkg/injector/patcher"
	"github.com/dapr/dapr/pkg/security/token"
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
	sentryAddress := patcher.ServiceSentry.Address(i.config.Namespace, i.config.KubeClusterDomain)
	operatorAddress := patcher.ServiceAPI.Address(i.config.Namespace, i.config.KubeClusterDomain)

	trustAnchors, err := i.currentTrustAnchors()
	if err != nil {
		return nil, err
	}
	daprdCert, daprdPrivateKey, err := i.signDaprdCertificate(ctx, ar.Request.Namespace)
	if err != nil {
		return nil, err
	}

	// Create the sidecar configuration object from the pod
	sidecar := patcher.NewSidecarConfig(pod)
	sidecar.GetInjectedComponentContainers = i.getInjectedComponentContainers
	sidecar.Mode = injectorConsts.ModeKubernetes
	sidecar.Namespace = ar.Request.Namespace
	sidecar.MTLSEnabled = mTLSEnabled(i.controlPlaneNamespace, i.daprClient)
	sidecar.Identity = ar.Request.Namespace + ":" + pod.Spec.ServiceAccountName
	sidecar.IgnoreEntrypointTolerations = i.config.GetIgnoreEntrypointTolerations()
	sidecar.ImagePullPolicy = i.config.GetPullPolicy()
	sidecar.OperatorAddress = operatorAddress
	sidecar.SentryAddress = sentryAddress
	sidecar.RunAsNonRoot = i.config.GetRunAsNonRoot()
	sidecar.ReadOnlyRootFilesystem = i.config.GetReadOnlyRootFilesystem()
	sidecar.SidecarDropALLCapabilities = i.config.GetDropCapabilities()
	sidecar.ControlPlaneNamespace = i.controlPlaneNamespace
	sidecar.ControlPlaneTrustDomain = i.controlPlaneTrustDomain
	sidecar.SentrySPIFFEID = i.sentrySPIFFEID.String()
	sidecar.CurrentTrustAnchors = trustAnchors
	sidecar.CertChain = string(daprdCert)
	sidecar.CertKey = string(daprdPrivateKey)
	sidecar.DisableTokenVolume = !token.HasKubernetesToken()

	// Set addresses for actor services
	// Even if actors are disabled, however, the placement-host-address flag will still be included if explicitly set in the annotation dapr.io/placement-host-address
	// So, if the annotation is already set, we accept that and also use placement for actors services
	if sidecar.PlacementAddress == "" {
		// Set configuration for the actors service
		actorsSvcName, actorsSvc := i.config.GetActorsService()
		actorsSvcAddr := actorsSvc.Address(i.config.Namespace, i.config.KubeClusterDomain)
		if actorsSvcName == "placement" {
			// If the actors service is "placement" (default if empty), then we use the PlacementAddress option for backwards compatibility
			// TODO: In the future, use the actors-service CLI flag too
			sidecar.ActorsService = ""
			sidecar.PlacementAddress = actorsSvcAddr
		} else {
			// We have a different actors service, not placement
			// Set the actors-service CLI flag with "<name>:<address>"
			sidecar.ActorsService = actorsSvcName + ":" + actorsSvcAddr
		}
	} else {
		// If we are using placement forcefully, do not set "ActorsService"
		sidecar.ActorsService = ""
	}

	// Set address for reminders service
	// This could be empty, indicating sidecars to use the built-in reminders subsystem
	remindersSvcName, remindersSvc, useRemindersSvc := i.config.GetRemindersService()
	if useRemindersSvc {
		// Set the reminders-service CLI flag with "<name>:<address>"
		sidecar.RemindersService = remindersSvcName + ":" + remindersSvc.Address(i.config.Namespace, i.config.KubeClusterDomain)
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

func mTLSEnabled(controlPlaneNamespace string, daprClient scheme.Interface) bool {
	resp, err := daprClient.ConfigurationV1alpha1().
		Configurations(controlPlaneNamespace).
		Get(defaultConfig, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("Dapr system configuration '%s' does not exist; using default value %t for mTLSEnabled", defaultConfig, defaultMtlsEnabled)
		return defaultMtlsEnabled
	}

	if err != nil {
		log.Errorf("Failed to load dapr configuration from k8s, use default value %t for mTLSEnabled: %s", defaultMtlsEnabled, err)
		return defaultMtlsEnabled
	}

	return resp.Spec.MTLSSpec.GetEnabled()
}
