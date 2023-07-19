package injector

import (
	"strconv"

	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/injector/patcher"
	"github.com/dapr/dapr/pkg/validation"
)

// GetPatch returns the patch to apply to a Pod to inject the Dapr sidecar
func (c *SidecarConfig) GetPatch() (patchOps jsonpatch.Patch, err error) {
	// TODO: In caller, set defaults for PlacementServiceAddress and SidecarImage
	// We still include PlacementServiceAddress if explicitly set as annotation
	/*if cfg.Annotations.Exist(annotations.KeyPlacementHostAddresses) {
		cfg.PlacementServiceAddress = cfg.Annotations.GetString(annotations.KeyPlacementHostAddresses)
	} else if cfg.SkipPlacement {
		cfg.PlacementServiceAddress = ""
	}
	if image := cfg.Annotations.GetString(annotations.KeySidecarImage); image != "" {
		cfg.DaprSidecarImage = image
	}*/

	// If Dapr is not enabled, or if the daprd container is already present, return
	if !c.Enabled || c.podContainsSidecarContainer() {
		return nil, nil
	}

	// Validate AppID
	err = validation.ValidateKubernetesAppID(c.GetAppID())
	if err != nil {
		return nil, err
	}

	patchOps = jsonpatch.Patch{}

	// Get volume mounts and add the UDS volume mount if needed
	volumeMounts := c.GetVolumeMounts()
	socketVolumeMount := c.GetUnixDomainSocketVolumeMount()
	if socketVolumeMount != nil {
		volumeMounts = append(volumeMounts, *socketVolumeMount)
	}

	// Pluggable components
	appContainers, componentContainers := c.SplitContainers()
	if err != nil {
		return nil, err
	}
	var injectedComponentContainers []corev1.Container
	if c.GetInjectedComponentContainers != nil && c.InjectPluggableComponents {
		injectedComponentContainers, err = c.GetInjectedComponentContainers(c.GetAppID(), c.Namespace)
		if err != nil {
			return nil, err
		}
	}
	componentPatchOps, componentsSocketVolumeMount := c.ComponentsPatchOps(componentContainers, injectedComponentContainers)

	// Projected volume with the token
	if !c.DisableTokenVolume {
		tokenVolume := c.GetTokenVolume()
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      TokenVolumeName,
			MountPath: TokenVolumeKubernetesMountPath,
			ReadOnly:  true,
		})

		patchOps = append(patchOps, GetVolumesPatchOperations(
			c.pod.Spec.Volumes,
			[]corev1.Volume{tokenVolume},
			patcher.PatchPathVolumes,
		)...)
	}

	// Get the sidecar container
	sidecarContainer, err := c.GetSidecarContainer(getSidecarContainerOpts{
		ComponentsSocketsVolumeMount: componentsSocketVolumeMount,
		VolumeMounts:                 volumeMounts,
	})
	if err != nil {
		return nil, err
	}

	// Create the list of patch operations
	if len(c.pod.Spec.Containers) == 0 {
		// Set to empty to support add operations individually
		patchOps = append(patchOps,
			patcher.NewPatchOperation("add", patcher.PatchPathContainers, []corev1.Container{}),
		)
	}

	patchOps = append(patchOps,
		patcher.NewPatchOperation("add", patcher.PatchPathContainers+"/-", sidecarContainer),
		c.AddDaprSidecarInjectedLabel(),
		c.AddDaprSidecarAppIDLabel(),
		c.AddDaprSidecarMetricsEnabledLabel(),
	)

	patchOps = append(patchOps,
		c.AddDaprEnvVarsToContainers(appContainers, c.GetAppProtocol())...,
	)
	patchOps = append(patchOps,
		AddSocketVolumeMountToContainers(appContainers, socketVolumeMount)...,
	)
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

// AddDaprEnvVarsToContainers adds Dapr environment variables to all the containers in any Dapr-enabled pod.
// The containers can be injected or user-defined.
func (c *SidecarConfig) AddDaprEnvVarsToContainers(containers map[int]corev1.Container, appProtocol string) jsonpatch.Patch {
	envPatchOps := make(jsonpatch.Patch, 0, len(containers)*2)
	envVars := []corev1.EnvVar{
		{
			Name:  UserContainerDaprHTTPPortName,
			Value: strconv.FormatInt(int64(c.SidecarHTTPPort), 10),
		},
		{
			Name:  UserContainerDaprGRPCPortName,
			Value: strconv.FormatInt(int64(c.SidecarAPIGRPCPort), 10),
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

// AddDaprSidecarInjectedLabel adds Dapr label to patch pod so list of patched pods can be retrieved more efficiently
func (c *SidecarConfig) AddDaprSidecarInjectedLabel() jsonpatch.Operation {
	if len(c.pod.Labels) == 0 { // empty labels
		return patcher.NewPatchOperation("add", PatchPathLabels, map[string]string{
			SidecarInjectedLabel: "true",
		})
	}

	return patcher.NewPatchOperation("add", PatchPathLabels+"/dapr.io~1sidecar-injected", "true")
}

// AddDaprSidecarAppIDLabel adds Dapr app-id label which can be handy for metric labels
func (c *SidecarConfig) AddDaprSidecarAppIDLabel() jsonpatch.Operation {
	if len(c.pod.Labels) == 0 { // empty labels
		return patcher.NewPatchOperation("add", PatchPathLabels, map[string]string{
			SidecarAppIDLabel: c.GetAppID(),
		})
	}
	return patcher.NewPatchOperation("add", PatchPathLabels+"/dapr.io~1app-id", c.GetAppID())
}

// AddDaprSidecarMetricsEnabledLabel adds Dapr metrics-enabled label which can be handy for scraping metrics
func (c *SidecarConfig) AddDaprSidecarMetricsEnabledLabel() jsonpatch.Operation {
	if len(c.pod.Labels) == 0 { // empty labels
		return patcher.NewPatchOperation("add", PatchPathLabels, map[string]string{
			SidecarMetricsEnabledLabel: strconv.FormatBool(c.EnableMetrics),
		})
	}
	return patcher.NewPatchOperation("add", PatchPathLabels+"/dapr.io~1metrics-enabled", strconv.FormatBool(c.EnableMetrics))
}
