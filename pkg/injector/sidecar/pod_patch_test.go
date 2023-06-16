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
	"encoding/json"
	"strconv"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/patcher"
)

func TestAddDaprEnvVarsToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer coreV1.Container
		appProtocol   string
		expOpsLen     int
		expOps        []patcher.PatchOperation
	}{
		{
			testName: "empty environment vars",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
			},
			expOpsLen: 1,
			expOps: []patcher.PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env",
					Value: []coreV1.EnvVar{
						{
							Name:  UserContainerDaprHTTPPortName,
							Value: strconv.Itoa(SidecarHTTPPort),
						},
						{
							Name:  UserContainerDaprGRPCPortName,
							Value: strconv.Itoa(SidecarAPIGRPCPort),
						},
					},
				},
			},
		},
		{
			testName: "existing env var",
			mockContainer: coreV1.Container{
				Name: "Mock Container",
				Env: []coreV1.EnvVar{
					{
						Name:  "TEST",
						Value: "Existing value",
					},
				},
			},
			expOpsLen: 2,
			expOps: []patcher.PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: coreV1.EnvVar{
						Name:  UserContainerDaprHTTPPortName,
						Value: strconv.Itoa(SidecarHTTPPort),
					},
				},
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: coreV1.EnvVar{
						Name:  UserContainerDaprGRPCPortName,
						Value: strconv.Itoa(SidecarAPIGRPCPort),
					},
				},
			},
		},
		{
			testName: "existing conflicting env var",
			mockContainer: coreV1.Container{
				Name: "Mock Container",
				Env: []coreV1.EnvVar{
					{
						Name:  "TEST",
						Value: "Existing value",
					},
					{
						Name:  UserContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 1,
			expOps: []patcher.PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: coreV1.EnvVar{
						Name:  UserContainerDaprHTTPPortName,
						Value: strconv.Itoa(SidecarHTTPPort),
					},
				},
			},
		},
		{
			testName: "multiple existing conflicting env vars",
			mockContainer: coreV1.Container{
				Name: "Mock Container",
				Env: []coreV1.EnvVar{
					{
						Name:  UserContainerDaprHTTPPortName,
						Value: "3510",
					},
					{
						Name:  UserContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 0,
			expOps:    []patcher.PatchOperation{},
		},
		{
			testName: "with app protocol",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
			},
			expOpsLen:   1,
			appProtocol: "h2c",
			expOps: []patcher.PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env",
					Value: []coreV1.EnvVar{
						{
							Name:  UserContainerDaprHTTPPortName,
							Value: strconv.Itoa(SidecarHTTPPort),
						},
						{
							Name:  UserContainerDaprGRPCPortName,
							Value: strconv.Itoa(SidecarAPIGRPCPort),
						},
						{
							Name:  UserContainerAppProtocolName,
							Value: "h2c",
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			patchEnv := AddDaprEnvVarsToContainers(map[int]coreV1.Container{0: tc.mockContainer}, tc.appProtocol)
			assert.Equal(t, tc.expOpsLen, len(patchEnv))
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}

func TestAddDaprAppIDLabel(t *testing.T) {
	testCases := []struct {
		testName  string
		mockPod   coreV1.Pod
		expLabels map[string]string
	}{
		{
			testName: "empty labels",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expLabels: map[string]string{SidecarAppIDLabel: "my-app"},
		},
		{
			testName: "with some previous labels",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
			},
			expLabels: map[string]string{SidecarAppIDLabel: "my-app", "app": "my-app"},
		},
		{
			testName: "with dapr app-id label already present",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{SidecarAppIDLabel: "my-app", "app": "my-app"},
				},
			},
			expLabels: map[string]string{SidecarAppIDLabel: "my-app", "app": "my-app"},
		},
	}

	for _, tc := range testCases {
		tc := tc // closure copy
		t.Run(tc.testName, func(t *testing.T) {
			newPodJSON := patchObject(t, tc.mockPod, []patcher.PatchOperation{AddDaprSideCarAppIDLabel("my-app", tc.mockPod.Labels)})
			newPod := coreV1.Pod{}
			assert.NoError(t, json.Unmarshal(newPodJSON, &newPod))
			assert.Equal(t, tc.expLabels, newPod.Labels)
		})
	}
}

func TestAddDaprMetricsEnabledLabel(t *testing.T) {
	testCases := []struct {
		testName       string
		mockPod        coreV1.Pod
		expLabels      map[string]string
		metricsEnabled bool
	}{
		{
			testName: "metrics annotation not present, fallback to default",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expLabels:      map[string]string{SidecarMetricsEnabledLabel: strconv.FormatBool(annotations.DefaultEnableMetric)},
			metricsEnabled: annotations.DefaultEnableMetric,
		},
		{
			testName: "metrics annotation present and explicitly enabled, with existing labels",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{SidecarMetricsEnabledLabel: "true"},
					Labels:      map[string]string{"app": "my-app"},
				},
			},
			expLabels:      map[string]string{SidecarMetricsEnabledLabel: "true", "app": "my-app"},
			metricsEnabled: true,
		},
		{
			testName: "metrics annotation present and explicitly disabled",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{SidecarMetricsEnabledLabel: "false"},
				},
			},
			expLabels:      map[string]string{SidecarMetricsEnabledLabel: "false"},
			metricsEnabled: false,
		},
	}

	for _, tc := range testCases {
		tc := tc // closure copy
		t.Run(tc.testName, func(t *testing.T) {
			newPodJSON := patchObject(t, tc.mockPod, []patcher.PatchOperation{AddDaprSideCarMetricsEnabledLabel(tc.metricsEnabled, tc.mockPod.Labels)})
			newPod := coreV1.Pod{}
			assert.NoError(t, json.Unmarshal(newPodJSON, &newPod))
			assert.Equal(t, tc.expLabels, newPod.Labels)
		})
	}
}

func TestAddDaprInjectedLabel(t *testing.T) {
	testCases := []struct {
		testName  string
		mockPod   coreV1.Pod
		expLabels map[string]string
	}{
		{
			testName: "empty labels",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expLabels: map[string]string{SidecarInjectedLabel: "true"},
		},
		{
			testName: "with some previous labels",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
			},
			expLabels: map[string]string{SidecarInjectedLabel: "true", "app": "my-app"},
		},
		{
			testName: "with dapr injected label already present",
			mockPod: coreV1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{SidecarInjectedLabel: "true", "app": "my-app"},
				},
			},
			expLabels: map[string]string{SidecarInjectedLabel: "true", "app": "my-app"},
		},
	}

	for _, tc := range testCases {
		tc := tc // closure copy
		t.Run(tc.testName, func(t *testing.T) {
			newPodJSON := patchObject(t, tc.mockPod, []patcher.PatchOperation{AddDaprSideCarInjectedLabel(tc.mockPod.Labels)})
			newPod := coreV1.Pod{}
			assert.NoError(t, json.Unmarshal(newPodJSON, &newPod))
			assert.Equal(t, tc.expLabels, newPod.Labels)
		})
	}
}

// patchObject executes a jsonpatch action against the object passed
func patchObject(t *testing.T, origObj interface{}, patchOperations []patcher.PatchOperation) []byte {
	marshal := func(o interface{}) []byte {
		objBytes, err := json.Marshal(o)
		assert.NoError(t, err)
		return objBytes
	}

	podJSON := marshal(origObj)
	patchJSON := marshal(patchOperations)
	decodedPatch, err := jsonpatch.DecodePatch(patchJSON)
	assert.NoError(t, err)
	newJSON, err := decodedPatch.Apply(podJSON)
	assert.NoError(t, err)
	return newJSON
}

func TestAddSocketVolumeToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer coreV1.Container
		socketMount   *coreV1.VolumeMount
		expOpsLen     int
		expOps        []patcher.PatchOperation
	}{
		{
			testName: "empty var, empty volume",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
			},
			socketMount: nil,
			expOpsLen:   0,
			expOps:      []patcher.PatchOperation{},
		},
		{
			testName: "existing var, empty volume",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
			},
			socketMount: &coreV1.VolumeMount{
				Name:      UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []patcher.PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts",
					Value: []coreV1.VolumeMount{{
						Name:      UnixDomainSocketVolume,
						MountPath: "/tmp",
					}},
				},
			},
		},
		{
			testName: "existing var, existing volume",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
				VolumeMounts: []coreV1.VolumeMount{
					{Name: "mock1"},
				},
			},
			socketMount: &coreV1.VolumeMount{
				Name:      UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []patcher.PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts/-",
					Value: coreV1.VolumeMount{
						Name:      UnixDomainSocketVolume,
						MountPath: "/tmp",
					},
				},
			},
		},
		{
			testName: "existing var, multiple existing volumes",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
				VolumeMounts: []coreV1.VolumeMount{
					{Name: "mock1"},
					{Name: "mock2"},
				},
			},
			socketMount: &coreV1.VolumeMount{
				Name:      UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []patcher.PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts/-",
					Value: coreV1.VolumeMount{
						Name:      UnixDomainSocketVolume,
						MountPath: "/tmp",
					},
				},
			},
		},
		{
			testName: "existing var, conflict volume name",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
				VolumeMounts: []coreV1.VolumeMount{
					{Name: UnixDomainSocketVolume},
				},
			},
			socketMount: &coreV1.VolumeMount{
				Name:      UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    []patcher.PatchOperation{},
		},
		{
			testName: "existing var, conflict volume mount path",
			mockContainer: coreV1.Container{
				Name: "MockContainer",
				VolumeMounts: []coreV1.VolumeMount{
					{MountPath: "/tmp"},
				},
			},
			socketMount: &coreV1.VolumeMount{
				Name:      UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    []patcher.PatchOperation{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			patchEnv := AddSocketVolumeMountToContainers(map[int]coreV1.Container{0: tc.mockContainer}, tc.socketMount)
			assert.Equal(t, tc.expOpsLen, len(patchEnv))
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}

func TestPodContainsVolume(t *testing.T) {
	testCases := []struct {
		testName   string
		podVolumes []coreV1.Volume
		volumeName string
		expect     bool
	}{
		{
			"pod with no volumes",
			[]coreV1.Volume{},
			"volume1",
			false,
		},
		{
			"pod does not contain volume",
			[]coreV1.Volume{
				{Name: "volume"},
			},
			"volume1",
			false,
		},
		{
			"pod contains volume",
			[]coreV1.Volume{
				{Name: "volume1"},
				{Name: "volume2"},
			},
			"volume2",
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := coreV1.Pod{
				Spec: coreV1.PodSpec{
					Volumes: tc.podVolumes,
				},
			}
			assert.Equal(t, tc.expect, podContainsVolume(pod, tc.volumeName))
		})
	}
}

func TestGetVolumeMounts(t *testing.T) {
	testCases := []struct {
		testName                  string
		volumeReadOnlyAnnotation  string
		volumeReadWriteAnnotation string
		podVolumeMountNames       []string
		expVolumeMounts           []coreV1.VolumeMount
	}{
		{
			"no annotations",
			"",
			"",
			[]string{"mount1", "mount2"},
			[]coreV1.VolumeMount{},
		},
		{
			"annotations with volumes present in pod",
			"mount1:/tmp/mount1,mount2:/tmp/mount2",
			"mount3:/tmp/mount3,mount4:/tmp/mount4",
			[]string{"mount1", "mount2", "mount3", "mount4"},
			[]coreV1.VolumeMount{
				{Name: "mount1", MountPath: "/tmp/mount1", ReadOnly: true},
				{Name: "mount2", MountPath: "/tmp/mount2", ReadOnly: true},
				{Name: "mount3", MountPath: "/tmp/mount3", ReadOnly: false},
				{Name: "mount4", MountPath: "/tmp/mount4", ReadOnly: false},
			},
		},
		{
			"annotations with volumes not present in pod",
			"mount1:/tmp/mount1,mount2:/tmp/mount2",
			"mount3:/tmp/mount3,mount4:/tmp/mount4",
			[]string{"mount1", "mount2", "mount4"},
			[]coreV1.VolumeMount{
				{Name: "mount1", MountPath: "/tmp/mount1", ReadOnly: true},
				{Name: "mount2", MountPath: "/tmp/mount2", ReadOnly: true},
				{Name: "mount4", MountPath: "/tmp/mount4", ReadOnly: false},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := coreV1.Pod{}
			pod.Annotations = map[string]string{
				annotations.KeyVolumeMountsReadOnly:  tc.volumeReadOnlyAnnotation,
				annotations.KeyVolumeMountsReadWrite: tc.volumeReadWriteAnnotation,
			}
			pod.Spec.Volumes = []coreV1.Volume{}
			for _, volumeName := range tc.podVolumeMountNames {
				pod.Spec.Volumes = append(pod.Spec.Volumes, coreV1.Volume{Name: volumeName})
			}

			volumeMounts := GetVolumeMounts(pod)
			assert.Equal(t, tc.expVolumeMounts, volumeMounts)
		})
	}
}
