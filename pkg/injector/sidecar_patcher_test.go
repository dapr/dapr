package injector

import (
	"encoding/json"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/injector/patcher"
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
				patcher.NewPatchOperation("add", "/spec/containers/0/env", []corev1.EnvVar{
					{
						Name:  UserContainerDaprHTTPPortName,
						Value: "3500",
					},
					{
						Name:  UserContainerDaprGRPCPortName,
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
				patcher.NewPatchOperation("add", "/spec/containers/0/env/-", corev1.EnvVar{
					Name:  UserContainerDaprHTTPPortName,
					Value: "3500",
				}),
				patcher.NewPatchOperation("add", "/spec/containers/0/env/-", corev1.EnvVar{
					Name:  UserContainerDaprGRPCPortName,
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
						Name:  UserContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 1,
			expOps: jsonpatch.Patch{
				patcher.NewPatchOperation("add", "/spec/containers/0/env/-", corev1.EnvVar{
					Name:  UserContainerDaprHTTPPortName,
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
				patcher.NewPatchOperation("add", "/spec/containers/0/env", []corev1.EnvVar{
					{
						Name:  UserContainerDaprHTTPPortName,
						Value: "3500",
					},
					{
						Name:  UserContainerDaprGRPCPortName,
						Value: "50001",
					},
					{
						Name:  UserContainerAppProtocolName,
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
			expLabels: map[string]string{SidecarAppIDLabel: "my-app"},
		},
		{
			testName: "with some previous labels",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
			},
			expLabels: map[string]string{SidecarAppIDLabel: "my-app", "app": "my-app"},
		},
		{
			testName: "with dapr app-id label already present",
			mockPod: corev1.Pod{
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
			newPodJSON := patchObject(t, tc.mockPod, jsonpatch.Patch{
				AddDaprSidecarAppIDLabel("my-app", tc.mockPod.Labels),
			})
			newPod := corev1.Pod{}
			assert.NoError(t, json.Unmarshal(newPodJSON, &newPod))
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
			expLabels:      map[string]string{SidecarMetricsEnabledLabel: "false"},
			metricsEnabled: false,
		},
		{
			testName: "metrics annotation present and explicitly enabled, with existing labels",
			mockPod: corev1.Pod{
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
			mockPod: corev1.Pod{
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
			newPodJSON := patchObject(t, tc.mockPod, jsonpatch.Patch{
				AddDaprSidecarMetricsEnabledLabel(tc.metricsEnabled, tc.mockPod.Labels),
			})
			newPod := corev1.Pod{}
			assert.NoError(t, json.Unmarshal(newPodJSON, &newPod))
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
			expLabels: map[string]string{SidecarInjectedLabel: "true"},
		},
		{
			testName: "with some previous labels",
			mockPod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "my-app"},
				},
			},
			expLabels: map[string]string{SidecarInjectedLabel: "true", "app": "my-app"},
		},
		{
			testName: "with dapr injected label already present",
			mockPod: corev1.Pod{
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
			newPodJSON := patchObject(t, tc.mockPod, jsonpatch.Patch{
				AddDaprSidecarInjectedLabel(tc.mockPod.Labels)},
			)
			newPod := corev1.Pod{}
			assert.NoError(t, json.Unmarshal(newPodJSON, &newPod))
			assert.Equal(t, tc.expLabels, newPod.Labels)
		})
	}
}
