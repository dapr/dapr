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
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/injector/annotations"
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
			patchEnv := c.addDaprEnvVarsToContainers(map[int]corev1.Container{0: tc.mockContainer}, tc.appProtocol)
			assert.Len(t, patchEnv, tc.expOpsLen)
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}

func TestPodNeedsPatching(t *testing.T) {
	tests := []struct {
		name string
		want bool
		pod  *corev1.Pod
	}{
		{
			name: "false if enabled annotation is missing",
			want: false,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "false if enabled annotation is falsey",
			want: false,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyEnabled: "0",
					},
				},
			},
		},
		{
			name: "true if enabled annotation is truthy",
			want: true,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyEnabled: "yes",
					},
				},
			},
		},
		{
			name: "false if daprd container already exists",
			want: false,
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyEnabled: "yes",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "daprd"},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewSidecarConfig(tt.pod)
			c.SetFromPodAnnotations()

			got := c.NeedsPatching()
			if got != tt.want {
				t.Errorf("SidecarConfig.NeedsPatching() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPatching(t *testing.T) {
	assertDaprdContainerFn := func(t *testing.T, pod *corev1.Pod) {
		t.Helper()

		assert.Len(t, pod.Spec.Containers, 2)
		assert.Equal(t, "appcontainer", pod.Spec.Containers[0].Name)
		assert.Equal(t, "daprd", pod.Spec.Containers[1].Name)

		assert.Equal(t, "/daprd", pod.Spec.Containers[1].Args[0])
	}

	type testCase struct {
		name                    string
		podModifierFn           func(pod *corev1.Pod)
		sidecarConfigModifierFn func(c *SidecarConfig)
		assertFn                func(t *testing.T, pod *corev1.Pod)
	}
	testCaseFn := func(tc testCase) func(t *testing.T) {
		return func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "myapp",
					Annotations: map[string]string{
						"dapr.io/enabled": "true",
						"dapr.io/app-id":  "myapp",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "appcontainer",
							Image: "container:1.0",
							Env: []corev1.EnvVar{
								{Name: "CIAO", Value: "mondo"},
							},
						},
					},
				},
			}
			if tc.podModifierFn != nil {
				tc.podModifierFn(pod)
			}

			c := NewSidecarConfig(pod)
			c.Namespace = "testns"
			c.Identity = "pod:identity"
			c.CertChain = "certchain"
			c.CertKey = "certkey"
			c.SentrySPIFFEID = "spiffe://foo.bar/ns/example/dapr-sentry"

			if tc.sidecarConfigModifierFn != nil {
				tc.sidecarConfigModifierFn(c)
			}

			c.SetFromPodAnnotations()

			patch, err := c.GetPatch()
			require.NoError(t, err)

			newPod, err := PatchPod(pod, patch)
			require.NoError(t, err)

			tc.assertFn(t, newPod)
		}
	}

	tests := []testCase{
		{
			name: "basic test",
			assertFn: func(t *testing.T, pod *corev1.Pod) {
				assertDaprdContainerFn(t, pod)

				// Assertions around the Daprd container
				daprdContainer := pod.Spec.Containers[1]
				assert.Equal(t, "/daprd", daprdContainer.Args[0])

				daprdEnvVars := map[string]string{}
				for _, env := range daprdContainer.Env {
					daprdEnvVars[env.Name] = env.Value
				}
				assert.Equal(t, "testns", daprdEnvVars["NAMESPACE"])
				assert.Equal(t, "pod:identity", daprdEnvVars["SENTRY_LOCAL_IDENTITY"])

				assert.Len(t, daprdContainer.VolumeMounts, 1)
				assert.Equal(t, "dapr-identity-token", daprdContainer.VolumeMounts[0].Name)
				assert.Equal(t, "/var/run/secrets/dapr.io/sentrytoken", daprdContainer.VolumeMounts[0].MountPath)
				assert.True(t, daprdContainer.VolumeMounts[0].ReadOnly)

				assert.NotNil(t, daprdContainer.LivenessProbe)
				assert.Equal(t, "/v1.0/healthz", daprdContainer.LivenessProbe.HTTPGet.Path)
				assert.Equal(t, 3501, daprdContainer.LivenessProbe.HTTPGet.Port.IntValue())

				// Assertions on added volumes
				assert.Len(t, pod.Spec.Volumes, 1)
				tokenVolume := pod.Spec.Volumes[0]
				assert.Equal(t, "dapr-identity-token", tokenVolume.Name)
				assert.NotNil(t, tokenVolume.Projected)
				require.Len(t, tokenVolume.Projected.Sources, 1)
				require.NotNil(t, tokenVolume.Projected.Sources[0].ServiceAccountToken)
				assert.Equal(t, "spiffe://foo.bar/ns/example/dapr-sentry", tokenVolume.Projected.Sources[0].ServiceAccountToken.Audience)

				// Assertions on added labels
				assert.Equal(t, "true", pod.Labels[injectorConsts.SidecarInjectedLabel])
				assert.Equal(t, "myapp", pod.Labels[injectorConsts.SidecarAppIDLabel])
				assert.Equal(t, "true", pod.Labels[injectorConsts.SidecarMetricsEnabledLabel])

				// Assertions on added envs
				appEnvVars := map[string]string{}
				for _, env := range pod.Spec.Containers[0].Env {
					appEnvVars[env.Name] = env.Value
				}
				assert.Equal(t, "mondo", appEnvVars["CIAO"])
				assert.Equal(t, "3500", appEnvVars["DAPR_HTTP_PORT"])
				assert.Equal(t, "50001", appEnvVars["DAPR_GRPC_PORT"])
				assert.Equal(t, "http", appEnvVars["APP_PROTOCOL"])
			},
		},
		{
			name: "with UDS",
			podModifierFn: func(pod *corev1.Pod) {
				pod.Annotations[annotations.KeyUnixDomainSocketPath] = "/tmp/socket"
			},
			assertFn: func(t *testing.T, pod *corev1.Pod) {
				assertDaprdContainerFn(t, pod)

				// Check the presence of the volume
				assert.Len(t, pod.Spec.Volumes, 2)
				socketVolume := pod.Spec.Volumes[0]
				assert.Equal(t, "dapr-unix-domain-socket", socketVolume.Name)
				assert.NotNil(t, socketVolume.EmptyDir)
				assert.Equal(t, corev1.StorageMediumMemory, socketVolume.EmptyDir.Medium)
				tokenVolume := pod.Spec.Volumes[1]
				assert.Equal(t, "dapr-identity-token", tokenVolume.Name)
				assert.NotNil(t, tokenVolume.Projected)
				require.Len(t, tokenVolume.Projected.Sources, 1)
				require.NotNil(t, tokenVolume.Projected.Sources[0].ServiceAccountToken)
				assert.Equal(t, "spiffe://foo.bar/ns/example/dapr-sentry", tokenVolume.Projected.Sources[0].ServiceAccountToken.Audience)

				// Check the presence of the volume mount in the app container
				appContainer := pod.Spec.Containers[0]
				assert.Len(t, appContainer.VolumeMounts, 1)
				assert.Equal(t, "dapr-unix-domain-socket", appContainer.VolumeMounts[0].Name)
				assert.Equal(t, "/tmp/socket", appContainer.VolumeMounts[0].MountPath)

				// Check the presence of the volume mount in the daprd container
				daprdContainer := pod.Spec.Containers[1]
				assert.Len(t, daprdContainer.VolumeMounts, 2)
				assert.Equal(t, "dapr-unix-domain-socket", daprdContainer.VolumeMounts[0].Name)
				assert.Equal(t, "/var/run/dapr-sockets", daprdContainer.VolumeMounts[0].MountPath)

				// Ensure the CLI flag is set
				args := strings.Join(daprdContainer.Args, " ")
				assert.Contains(t, args, "--unix-domain-socket /var/run/dapr-sockets")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, testCaseFn(tc))
	}
}
