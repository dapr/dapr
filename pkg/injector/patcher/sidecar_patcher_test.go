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
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
)

func TestAddDaprEnvVarsToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer corev1.Container
		appProtocol   string
		expOpsLen     int
		expOps        jsonpatch.Patch
	}{
		{
			testName: "empty environment vars",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			expOpsLen: 1,
			expOps: jsonpatch.Patch{
				NewPatchOperation("add", PatchPathContainers+"/0/env", []corev1.EnvVar{
					{
						Name:  injectorConsts.UserContainerDaprHTTPPortName,
						Value: "3500",
					},
					{
						Name:  injectorConsts.UserContainerDaprGRPCPortName,
						Value: "50001",
					},
				}),
			},
		},
		{
			testName: "existing env var",
			mockContainer: corev1.Container{
				Name: "Mock Container",
				Env: []corev1.EnvVar{
					{
						Name:  "TEST",
						Value: "Existing value",
					},
				},
			},
			expOpsLen: 2,
			expOps: jsonpatch.Patch{
				NewPatchOperation("add", PatchPathContainers+"/0/env/-", corev1.EnvVar{
					Name:  injectorConsts.UserContainerDaprHTTPPortName,
					Value: "3500",
				}),
				NewPatchOperation("add", PatchPathContainers+"/0/env/-", corev1.EnvVar{
					Name:  injectorConsts.UserContainerDaprGRPCPortName,
					Value: "50001",
				}),
			},
		},
		{
			testName: "existing conflicting env var",
			mockContainer: corev1.Container{
				Name: "Mock Container",
				Env: []corev1.EnvVar{
					{
						Name:  "TEST",
						Value: "Existing value",
					},
					{
						Name:  injectorConsts.UserContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 1,
			expOps: jsonpatch.Patch{
				NewPatchOperation("add", PatchPathContainers+"/0/env/-", corev1.EnvVar{
					Name:  injectorConsts.UserContainerDaprHTTPPortName,
					Value: "3500",
				}),
			},
		},
		{
			testName: "multiple existing conflicting env vars",
			mockContainer: corev1.Container{
				Name: "Mock Container",
				Env: []corev1.EnvVar{
					{
						Name:  injectorConsts.UserContainerDaprHTTPPortName,
						Value: "3510",
					},
					{
						Name:  injectorConsts.UserContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 0,
			expOps:    jsonpatch.Patch{},
		},
		{
			testName: "with app protocol",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			expOpsLen:   1,
			appProtocol: "h2c",
			expOps: jsonpatch.Patch{
				NewPatchOperation("add", PatchPathContainers+"/0/env", []corev1.EnvVar{
					{
						Name:  injectorConsts.UserContainerDaprHTTPPortName,
						Value: "3500",
					},
					{
						Name:  injectorConsts.UserContainerDaprGRPCPortName,
						Value: "50001",
					},
					{
						Name:  injectorConsts.UserContainerAppProtocolName,
						Value: "h2c",
					},
				}),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{})
			patchEnv := c.AddDaprEnvVarsToContainers(map[int]corev1.Container{0: tc.mockContainer}, tc.appProtocol)
			assert.Equal(t, tc.expOpsLen, len(patchEnv))
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}

func TestAddDaprAppIDLabel(t *testing.T) {
	testCases := []struct {
		testName  string
		mockPod   corev1.Pod
		expLabels map[string]string
	}{
		{
			testName: "empty labels",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expLabels: map[string]string{injectorConsts.SidecarAppIDLabel: "my-app"},
		},
		{
			testName: "with some previous labels",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
			},
			expLabels: map[string]string{injectorConsts.SidecarAppIDLabel: "my-app", "app": "my-app"},
		},
		{
			testName: "with dapr app-id label already present",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{injectorConsts.SidecarAppIDLabel: "my-app", "app": "my-app"},
				},
			},
			expLabels: map[string]string{injectorConsts.SidecarAppIDLabel: "my-app", "app": "my-app"},
		},
	}

	for _, tc := range testCases {
		tc := tc // closure copy
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "my-app",
					Labels: tc.mockPod.Labels,
				},
			})
			newPod, err := PatchPod(&tc.mockPod, jsonpatch.Patch{
				c.AddDaprSidecarAppIDLabel(),
			})
			require.NoError(t, err)
			assert.Equal(t, tc.expLabels, newPod.Labels)
		})
	}
}

func TestAddDaprMetricsEnabledLabel(t *testing.T) {
	testCases := []struct {
		testName       string
		mockPod        corev1.Pod
		expLabels      map[string]string
		metricsEnabled bool
	}{
		{
			testName: "metrics annotation not present, fallback to default",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expLabels:      map[string]string{injectorConsts.SidecarMetricsEnabledLabel: "false"},
			metricsEnabled: false,
		},
		{
			testName: "metrics annotation present and explicitly enabled, with existing labels",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{injectorConsts.SidecarMetricsEnabledLabel: "true"},
					Labels:      map[string]string{"app": "my-app"},
				},
			},
			expLabels:      map[string]string{injectorConsts.SidecarMetricsEnabledLabel: "true", "app": "my-app"},
			metricsEnabled: true,
		},
		{
			testName: "metrics annotation present and explicitly disabled",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{injectorConsts.SidecarMetricsEnabledLabel: "false"},
				},
			},
			expLabels:      map[string]string{injectorConsts.SidecarMetricsEnabledLabel: "false"},
			metricsEnabled: false,
		},
	}

	for _, tc := range testCases {
		tc := tc // closure copy
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: tc.mockPod.Labels,
				},
			})
			c.EnableMetrics = tc.metricsEnabled
			newPod, err := PatchPod(&tc.mockPod, jsonpatch.Patch{
				c.AddDaprSidecarMetricsEnabledLabel(),
			})
			require.NoError(t, err)
			assert.Equal(t, tc.expLabels, newPod.Labels)
		})
	}
}

func TestAddDaprInjectedLabel(t *testing.T) {
	testCases := []struct {
		testName  string
		mockPod   corev1.Pod
		expLabels map[string]string
	}{
		{
			testName: "empty labels",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expLabels: map[string]string{injectorConsts.SidecarInjectedLabel: "true"},
		},
		{
			testName: "with some previous labels",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
			},
			expLabels: map[string]string{injectorConsts.SidecarInjectedLabel: "true", "app": "my-app"},
		},
		{
			testName: "with dapr injected label already present",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{injectorConsts.SidecarInjectedLabel: "true", "app": "my-app"},
				},
			},
			expLabels: map[string]string{injectorConsts.SidecarInjectedLabel: "true", "app": "my-app"},
		},
	}

	for _, tc := range testCases {
		tc := tc // closure copy
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: tc.mockPod.Labels,
				},
			})
			newPod, err := PatchPod(&tc.mockPod, jsonpatch.Patch{
				c.AddDaprSidecarInjectedLabel(),
			})
			require.NoError(t, err)
			assert.Equal(t, tc.expLabels, newPod.Labels)
		})
	}
}
