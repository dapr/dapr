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
	"strconv"

	jsonpatch "github.com/evanphx/json-patch/v5"
	corev1 "k8s.io/api/core/v1"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/dapr/pkg/validation"
)

// NeedsPatching returns true if patching is needed.
func (c *SidecarConfig) NeedsPatching() bool {
	return c.Enabled && !c.podContainsSidecarContainer()
}

// GetPatch returns the patch to apply to a Pod to inject the Dapr sidecar
func (c *SidecarConfig) GetPatch() (patchOps jsonpatch.Patch, err error) {
	// If Dapr is not enabled, or if the daprd container is already present, return
	if !c.NeedsPatching() {
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
	appContainers, componentContainers := c.splitContainers()
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
	componentPatchOps, componentsSocketVolumeMount := c.componentsPatchOps(componentContainers, injectedComponentContainers)

	// Projected volume with the token
	if !c.DisableTokenVolume {
		tokenVolume := c.GetTokenVolume()
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      injectorConsts.TokenVolumeName,
			MountPath: injectorConsts.TokenVolumeKubernetesMountPath,
			ReadOnly:  true,
		})

		patchOps = append(patchOps, GetVolumesPatchOperations(
			c.pod.Spec.Volumes,
			[]corev1.Volume{tokenVolume},
			PatchPathVolumes,
		)...)
	}

	// Get the sidecar container
	sidecarContainer, err := c.getSidecarContainer(getSidecarContainerOpts{
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
			NewPatchOperation("add", PatchPathContainers, []corev1.Container{}),
		)
	}

	patchOps = append(patchOps,
		NewPatchOperation("add", PatchPathContainers+"/-", sidecarContainer),
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
		if c.Name == injectorConsts.SidecarContainerName {
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
			Name:  injectorConsts.UserContainerDaprHTTPPortName,
			Value: strconv.FormatInt(int64(c.SidecarHTTPPort), 10),
		},
		{
			Name:  injectorConsts.UserContainerDaprGRPCPortName,
			Value: strconv.FormatInt(int64(c.SidecarAPIGRPCPort), 10),
		},
	}
	if appProtocol != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  injectorConsts.UserContainerAppProtocolName,
			Value: appProtocol,
		})
	}
	for i, container := range containers {
		patchOps := GetEnvPatchOperations(container.Env, envVars, i)
		envPatchOps = append(envPatchOps, patchOps...)
	}
	return envPatchOps
}

// AddDaprSidecarInjectedLabel adds Dapr label to patch pod so list of patched pods can be retrieved more efficiently
func (c *SidecarConfig) AddDaprSidecarInjectedLabel() jsonpatch.Operation {
	if len(c.pod.Labels) == 0 { // empty labels
		return NewPatchOperation("add", injectorConsts.PatchPathLabels, map[string]string{
			injectorConsts.SidecarInjectedLabel: "true",
		})
	}

	return NewPatchOperation("add", injectorConsts.PatchPathLabels+"/dapr.io~1sidecar-injected", "true")
}

// AddDaprSidecarAppIDLabel adds Dapr app-id label which can be handy for metric labels
func (c *SidecarConfig) AddDaprSidecarAppIDLabel() jsonpatch.Operation {
	if len(c.pod.Labels) == 0 { // empty labels
		return NewPatchOperation("add", injectorConsts.PatchPathLabels, map[string]string{
			injectorConsts.SidecarAppIDLabel: c.GetAppID(),
		})
	}
	return NewPatchOperation("add", injectorConsts.PatchPathLabels+"/dapr.io~1app-id", c.GetAppID())
}

// AddDaprSidecarMetricsEnabledLabel adds Dapr metrics-enabled label which can be handy for scraping metrics
func (c *SidecarConfig) AddDaprSidecarMetricsEnabledLabel() jsonpatch.Operation {
	if len(c.pod.Labels) == 0 { // empty labels
		return NewPatchOperation("add", injectorConsts.PatchPathLabels, map[string]string{
			injectorConsts.SidecarMetricsEnabledLabel: strconv.FormatBool(c.EnableMetrics),
		})
	}
	return NewPatchOperation("add", injectorConsts.PatchPathLabels+"/dapr.io~1metrics-enabled", strconv.FormatBool(c.EnableMetrics))
}
