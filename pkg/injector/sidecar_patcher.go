package injector

import (
	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/injector/components"
	"github.com/dapr/dapr/pkg/injector/patcher"
	"github.com/dapr/dapr/pkg/validation"
)

// GetPatch returns the patch to apply to a Pod to inject the Dapr sidecar
func (c *SidecarConfig) GetPatch() (patchOps jsonpatch.Patch, err error) {
	// If Dapr is not enabled, or if the daprd container is already present, return
	if !c.Enabled || c.podContainsSidecarContainer() {
		return nil, nil
	}

	err = validation.ValidateKubernetesAppID(c.GetAppID())
	if err != nil {
		return nil, err
	}

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
	patchOps = jsonpatch.Patch{}
	if len(pod.Spec.Containers) == 0 {
		// Set to empty to support add operations individually
		patchOps = append(patchOps,
			patcher.NewPatchOperation("add", patcher.PatchPathContainers, []corev1.Container{}),
		)
	}

	patchOps = append(patchOps,
		patcher.NewPatchOperation("add", patcher.PatchPathContainers+"/-", sidecarContainer),
		sidecar.AddDaprSidecarInjectedLabel(pod.Labels),
		sidecar.AddDaprSidecarAppIDLabel(c.GetAppID(), c.pod.Labels),
		sidecar.AddDaprSidecarMetricsEnabledLabel(c.EnableMetrics, c.pod.Labels),
	)

	patchOps = append(patchOps,
		sidecar.AddDaprEnvVarsToContainers(appContainers, getAppProtocol(an))...,
	)
	patchOps = append(patchOps,
		sidecar.AddSocketVolumeMountToContainers(appContainers, socketVolumeMount)...,
	)
	volumePatchOps := sidecar.GetVolumesPatchOperations(
		pod.Spec.Volumes,
		[]corev1.Volume{tokenVolume},
		patcher.PatchPathVolumes,
	)
	patchOps = append(patchOps, volumePatchOps...)
	patchOps = append(patchOps, componentPatchOps...)

	return patchOps, nil
}

// podContainsSidecarContainer returns true if the pod contains a sidecar container (i.e. a container named "daprd").
func (c *SidecarConfig) podContainsSidecarContainer() bool {
	for _, c := range c.pod.Spec.Containers {
		if c.Name == SidecarContainerName {
			return true
		}
	}
	return false
}
