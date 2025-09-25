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
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/ptr"
)

func TestParseEnvString(t *testing.T) {
	testCases := []struct {
		testName string
		envStr   string
		expLen   int
		expKeys  []string
		expEnv   []corev1.EnvVar
	}{
		{
			testName: "empty environment string",
			envStr:   "",
			expLen:   0,
			expKeys:  []string{},
			expEnv:   []corev1.EnvVar{},
		},
		{
			testName: "common valid environment string",
			envStr:   "ENV1=value1 , ENV2=value2 , ENV3=value3",
			expLen:   3,
			expKeys:  []string{"ENV1", "ENV2", "ENV3"},
			expEnv: []corev1.EnvVar{
				{
					Name:  "ENV1",
					Value: "value1",
				},
				{
					Name:  "ENV2",
					Value: "value2",
				},
				{
					Name:  "ENV3",
					Value: "value3",
				},
			},
		},
		{
			testName: "environment string with quotes",
			envStr:   `HTTP_PROXY=http://myproxy.com, NO_PROXY="localhost,127.0.0.1,.amazonaws.com"`,
			expLen:   2,
			expKeys:  []string{"HTTP_PROXY", "NO_PROXY"},
			expEnv: []corev1.EnvVar{
				{
					Name:  "HTTP_PROXY",
					Value: "http://myproxy.com",
				},
				{
					Name:  "NO_PROXY",
					Value: `"localhost,127.0.0.1,.amazonaws.com"`,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{})
			c.Env = tc.envStr
			envKeys, envVars := c.getEnv()
			assert.Len(t, envVars, tc.expLen)
			assert.Equal(t, tc.expKeys, envKeys)
			assert.Equal(t, tc.expEnv, envVars)
		})
	}
}

func TestParseEnvFromSecret(t *testing.T) {
	testCases := []struct {
		testName     string
		envSecretStr string
		expLen       int
		expKeys      []string
		expEnv       []corev1.EnvVar
	}{
		{
			testName:     "empty env",
			envSecretStr: "",
			expLen:       0,
			expKeys:      []string{},
			expEnv:       []corev1.EnvVar{},
		},
		{
			testName:     "single key name value secret",
			envSecretStr: "KEY1=NAME1",
			expLen:       1,
			expKeys:      []string{"KEY1"},
			expEnv: []corev1.EnvVar{
				{
					Name: "KEY1",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "NAME1",
							},
							Key: "NAME1",
						},
					},
				},
			},
		},
		{
			testName:     "multi key name secret",
			envSecretStr: "KEY1=NAME1:SECRETKEY1",
			expLen:       1,
			expKeys:      []string{"KEY1"},
			expEnv: []corev1.EnvVar{
				{
					Name: "KEY1",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "NAME1",
							},
							Key: "SECRETKEY1",
						},
					},
				},
			},
		},
		{
			testName:     "multi key name value secret",
			envSecretStr: "KEY1=NAME1:SECRETKEY1,KEY2=NAME2:SECRETKEY2",
			expLen:       2,
			expKeys:      []string{"KEY1", "KEY2"},
			expEnv: []corev1.EnvVar{
				{
					Name: "KEY1",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "NAME1",
							},
							Key: "SECRETKEY1",
						},
					},
				},
				{
					Name: "KEY2",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "NAME2",
							},
							Key: "SECRETKEY2",
						},
					},
				},
			},
		},
		{
			testName:     "multi key name value secret with spaces",
			envSecretStr: "KEY1= NAME1 : SECRETKEY1, KEY2= NAME2 : SECRETKEY2",
			expLen:       2,
			expKeys:      []string{"KEY1", "KEY2"},
			expEnv: []corev1.EnvVar{
				{
					Name: "KEY1",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "NAME1",
							},
							Key: "SECRETKEY1",
						},
					},
				},
				{
					Name: "KEY2",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "NAME2",
							},
							Key: "SECRETKEY2",
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{})
			c.EnvFromSecret = tc.envSecretStr
			envKeys, envVars := c.getEnvFromSecret()
			assert.Len(t, envVars, tc.expLen)
			assert.Equal(t, tc.expKeys, envKeys)
			assert.Equal(t, tc.expEnv, envVars)
		})
	}
}

func TestGetResourceRequirements(t *testing.T) {
	t.Run("no resource requirements", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource limits", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:    "100m",
					annotations.KeyMemoryLimit: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Equal(t, "100m", r.Limits.Cpu().String())
		assert.Equal(t, "1Gi", r.Limits.Memory().String())
	})

	t.Run("invalid cpu limit", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:    "invalid",
					annotations.KeyMemoryLimit: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory limit", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:    "100m",
					annotations.KeyMemoryLimit: "invalid",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource requests", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPURequest:    "100m",
					annotations.KeyMemoryRequest: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Equal(t, "100m", r.Requests.Cpu().String())
		assert.Equal(t, "1Gi", r.Requests.Memory().String())
	})

	t.Run("invalid cpu request", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPURequest:    "invalid",
					annotations.KeyMemoryRequest: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory request", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPURequest:    "100m",
					annotations.KeyMemoryRequest: "invalid",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("limits and requests", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:      "200m",
					annotations.KeyMemoryLimit:   "2Gi",
					annotations.KeyCPURequest:    "100m",
					annotations.KeyMemoryRequest: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Equal(t, "200m", r.Limits.Cpu().String())
		assert.Equal(t, "2Gi", r.Limits.Memory().String())
		assert.Equal(t, "100m", r.Requests.Cpu().String())
		assert.Equal(t, "1Gi", r.Requests.Memory().String())
	})
}

func TestGetReadinessProbeHandler(t *testing.T) {
	pathElements := []string{"api", "v1", "healthz"}
	expectedPath := "/api/v1/healthz"
	expectedHandler := corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: expectedPath,
			Port: intstr.IntOrString{IntVal: 3500},
		},
	}

	assert.EqualValues(t, expectedHandler, getReadinessProbeHandler(3500, pathElements...))
}

func TestGetLivenessProbeHandler(t *testing.T) {
	expectedHandler := corev1.ProbeHandler{
		TCPSocket: &corev1.TCPSocketAction{
			Port: intstr.IntOrString{IntVal: 3500},
		},
	}

	assert.EqualValues(t, expectedHandler, getLivenessProbeHandler(3500))
}

func TestFormatProbePath(t *testing.T) {
	testCases := []struct {
		given    []string
		expected string
	}{
		{
			given:    []string{"api", "v1"},
			expected: "/api/v1",
		},
		{
			given:    []string{"//api", "v1"},
			expected: "/api/v1",
		},
		{
			given:    []string{"//api", "/v1/"},
			expected: "/api/v1",
		},
		{
			given:    []string{"//api", "/v1/", "healthz"},
			expected: "/api/v1/healthz",
		},
		{
			given:    []string{""},
			expected: "/",
		},
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.expected, formatProbePath(tc.given...))
	}
}

func TestGetSidecarContainer(t *testing.T) {
	// Allows running multiple test suites in a more DRY way
	type testCase struct {
		name                    string
		annotations             map[string]string
		podModifierFn           func(pod *corev1.Pod)
		sidecarConfigModifierFn func(c *SidecarConfig)
		assertFn                func(t *testing.T, container *corev1.Container)
		getSidecarContainerOpts getSidecarContainerOpts
	}
	testCaseFn := func(tc testCase) func(t *testing.T) {
		return func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tc.annotations,
				},
			}
			if tc.podModifierFn != nil {
				tc.podModifierFn(pod)
			}

			c := NewSidecarConfig(pod)
			c.AppID = "myapp"

			if tc.sidecarConfigModifierFn != nil {
				tc.sidecarConfigModifierFn(c)
			}

			c.SetFromPodAnnotations()

			container, err := c.getSidecarContainer(tc.getSidecarContainerOpts)
			require.NoError(t, err)

			tc.assertFn(t, container)
		}
	}
	testSuiteGenerator := func(tests []testCase) func(t *testing.T) {
		return func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, testCaseFn(tc))
			}
		}
	}

	t.Run("get sidecar container", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyAppID:          "app_id",
					annotations.KeyConfig:         "config",
					annotations.KeyAppPort:        "5000",
					annotations.KeyLogAsJSON:      "true",
					annotations.KeyAPITokenSecret: "secret",
					annotations.KeyAppTokenSecret: "appsecret",
				},
			},
		})
		//nolint:goconst
		c.SidecarImage = "daprio/dapr"
		c.ImagePullPolicy = "Always"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true
		c.Identity = "pod_identity"
		c.ControlPlaneNamespace = "my-namespace"
		c.ControlPlaneTrustDomain = "test.example.com"

		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{})
		require.NoError(t, err)

		expectedArgs := []string{
			"/daprd",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-id", "app_id",
			"--app-protocol", "http",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--app-port", "5000",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--placement-host-address", "placement:50000",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Empty(t, container.Command)
		assertEqualJSON(t, container.Env, `[{"name":"NAMESPACE","value":"dapr-system"},{"name":"DAPR_TRUST_ANCHORS"},{"name":"POD_NAME","valueFrom":{"fieldRef":{"fieldPath":"metadata.name"}}},{"name":"DAPR_CONTROLPLANE_NAMESPACE","value":"my-namespace"},{"name":"DAPR_CONTROLPLANE_TRUST_DOMAIN","value":"test.example.com"},{"name":"DAPR_API_TOKEN","valueFrom":{"secretKeyRef":{"name":"secret","key":"token"}}},{"name":"APP_API_TOKEN","valueFrom":{"secretKeyRef":{"name":"appsecret","key":"token"}}}]`)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with custom grpc ports", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyAppID:            "app_id",
					annotations.KeyConfig:           "config",
					annotations.KeyAppPort:          "5000",
					annotations.KeyLogAsJSON:        "true",
					annotations.KeyAPITokenSecret:   "secret",
					annotations.KeyAppTokenSecret:   "appsecret",
					annotations.KeyAPIGRPCPort:      "12345",
					annotations.KeyInternalGRPCPort: "12346",
				},
			},
		})
		c.SidecarImage = "daprio/dapr"
		c.ImagePullPolicy = "Always"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true
		c.Identity = "pod_identity"
		c.ControlPlaneNamespace = "my-namespace"
		c.ControlPlaneTrustDomain = "test.example.com"

		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{})
		require.NoError(t, err)

		expectedArgs := []string{
			"/daprd",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "12345",
			"--dapr-internal-grpc-port", "12346",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-id", "app_id",
			"--app-protocol", "http",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--app-port", "5000",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--placement-host-address", "placement:50000",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Empty(t, container.Command)
		assertEqualJSON(t, container.Env, `[{"name":"NAMESPACE","value":"dapr-system"},{"name":"DAPR_TRUST_ANCHORS"},{"name":"POD_NAME","valueFrom":{"fieldRef":{"fieldPath":"metadata.name"}}},{"name":"DAPR_CONTROLPLANE_NAMESPACE","value":"my-namespace"},{"name":"DAPR_CONTROLPLANE_TRUST_DOMAIN","value":"test.example.com"},{"name":"DAPR_API_TOKEN","valueFrom":{"secretKeyRef":{"name":"secret","key":"token"}}},{"name":"APP_API_TOKEN","valueFrom":{"secretKeyRef":{"name":"appsecret","key":"token"}}}]`)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with debugging", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyAppID:          "app_id",
					annotations.KeyConfig:         "config",
					annotations.KeyAppPort:        "5000",
					annotations.KeyLogAsJSON:      "true",
					annotations.KeyAPITokenSecret: "secret",
					annotations.KeyAppTokenSecret: "appsecret",
					annotations.KeyEnableDebug:    "true",
					annotations.KeyDebugPort:      "55555",
				},
			},
		})
		c.SidecarImage = "daprio/dapr"
		c.ImagePullPolicy = "Always"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true
		c.Identity = "pod_identity"
		c.ControlPlaneNamespace = "my-namespace"
		c.ControlPlaneTrustDomain = "test.example.com"
		c.EnableK8sDownwardAPIs = true

		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{})
		require.NoError(t, err)

		expectedArgs := []string{
			"/dlv",
			"--listen", ":55555",
			"--accept-multiclient",
			"--headless=true",
			"--log",
			"--api-version=2",
			"exec",
			"/daprd",
			"--",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-id", "app_id",
			"--app-protocol", "http",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--app-port", "5000",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--placement-host-address", "placement:50000",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Empty(t, container.Command)
		assertEqualJSON(t, container.Env, `[{"name":"NAMESPACE","value":"dapr-system"},{"name":"DAPR_TRUST_ANCHORS"},{"name":"POD_NAME","valueFrom":{"fieldRef":{"fieldPath":"metadata.name"}}},{"name":"DAPR_CONTROLPLANE_NAMESPACE","value":"my-namespace"},{"name":"DAPR_CONTROLPLANE_TRUST_DOMAIN","value":"test.example.com"},{"name":"DAPR_HOST_IP","valueFrom":{"fieldRef":{"fieldPath":"status.podIP"}}},{"name":"DAPR_API_TOKEN","valueFrom":{"secretKeyRef":{"name":"secret","key":"token"}}},{"name":"APP_API_TOKEN","valueFrom":{"secretKeyRef":{"name":"appsecret","key":"token"}}}]`)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("placement", testSuiteGenerator([]testCase{
		{
			name: "placement is included in options",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.PlacementAddress = "placement:1234"
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--placement-host-address placement:1234")
			},
		},
		{
			name: "placement is skipped in options",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.PlacementAddress = ""
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--placement-host-address")
			},
		},
		{
			name: "placement is skipped in options but included in annotations",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.PlacementAddress = ""
			},
			annotations: map[string]string{
				annotations.KeyPlacementHostAddresses: "some-host:50000",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--placement-host-address some-host:50000")
			},
		},
		{
			name: "placement is set in options and overrriden in annotations",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.PlacementAddress = "placement:1234"
			},
			annotations: map[string]string{
				annotations.KeyPlacementHostAddresses: "some-host:50000",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--placement-host-address some-host:50000")
			},
		},
	}))

	t.Run("listen address", testSuiteGenerator([]testCase{
		{
			name:        "default listen address",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--dapr-listen-addresses [::1],127.0.0.1")
			},
		},
		{
			name: "override listen address",
			annotations: map[string]string{
				annotations.KeySidecarListenAddresses: "1.2.3.4,[::1]",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--dapr-listen-addresses 1.2.3.4,[::1]")
			},
		},
	}))

	t.Run("graceful shutdown seconds", testSuiteGenerator([]testCase{
		{
			name:        "default to -1",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--dapr-graceful-shutdown-seconds -1")
			},
		},
		{
			name: "override the graceful shutdown",
			annotations: map[string]string{
				annotations.KeyGracefulShutdownSeconds: "3",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--dapr-graceful-shutdown-seconds 3")
			},
		},
		{
			name: "-1 when value is invalid",
			annotations: map[string]string{
				annotations.KeyGracefulShutdownSeconds: "invalid",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--dapr-graceful-shutdown-seconds -1")
			},
		},
	}))

	t.Run("block shutdown duration", testSuiteGenerator([]testCase{
		{
			name:        "default to empty",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--dapr-block-shutdown-duration")
			},
		},
		{
			name: "add a block shutdown duration",
			annotations: map[string]string{
				annotations.KeyBlockShutdownDuration: "3s",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--dapr-block-shutdown-duration 3s")
			},
		},
	}))

	t.Run("sidecar image", testSuiteGenerator([]testCase{
		{
			name:        "no annotation",
			annotations: map[string]string{},
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.SidecarImage = "daprio/dapr"
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Equal(t, "daprio/dapr", container.Image)
			},
		},
		{
			name: "override with annotation",
			annotations: map[string]string{
				annotations.KeySidecarImage: "override",
			},
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.SidecarImage = "daprio/dapr"
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Equal(t, "override", container.Image)
			},
		},
	}))

	t.Run("unix domain socket", testSuiteGenerator([]testCase{
		{
			name:        "default does not use UDS",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Empty(t, container.VolumeMounts)
			},
		},
		{
			name: "set UDS path",
			annotations: map[string]string{
				annotations.KeyUnixDomainSocketPath: "/tmp",
			},
			getSidecarContainerOpts: getSidecarContainerOpts{
				VolumeMounts: []corev1.VolumeMount{
					{Name: injectorConsts.UnixDomainSocketVolume, MountPath: "/tmp"},
				},
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Len(t, container.VolumeMounts, 1)
				assert.Equal(t, injectorConsts.UnixDomainSocketVolume, container.VolumeMounts[0].Name)
				assert.Equal(t, "/tmp", container.VolumeMounts[0].MountPath)
			},
		},
	}))

	t.Run("disable builtin K8s Secret Store", testCaseFn(testCase{
		annotations: map[string]string{
			annotations.KeyDisableBuiltinK8sSecretStore: "true",
		},
		assertFn: func(t *testing.T, container *corev1.Container) {
			assert.Contains(t, container.Args, "--disable-builtin-k8s-secret-store")
		},
	}))

	t.Run("test enable-api-logging", testSuiteGenerator([]testCase{
		{
			name:        "not set by default",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotContains(t, strings.Join(container.Args, " "), "--enable-api-logging")
			},
		},
		{
			name: "explicit true",
			annotations: map[string]string{
				annotations.KeyEnableAPILogging: "true",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Contains(t, container.Args, "--enable-api-logging=true")
			},
		},
		{
			name: "explicit false",
			annotations: map[string]string{
				annotations.KeyEnableAPILogging: "0",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Contains(t, container.Args, "--enable-api-logging=false")
			},
		},
		{
			name: "false when annotation is empty",
			annotations: map[string]string{
				annotations.KeyEnableAPILogging: "",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Contains(t, container.Args, "--enable-api-logging=false")
			},
		},
	}))

	t.Run("sidecar container should have the correct security context on Windows", testSuiteGenerator([]testCase{
		{
			name:        "windows security context is nil by default",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Nil(t, container.SecurityContext.WindowsOptions)
			},
		},
		{
			name: "setting SSL_CERT_DIR should set security context",
			annotations: map[string]string{
				annotations.KeyEnv: "SSL_CERT_DIR=/tmp/certificates",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotNil(t, container.SecurityContext.WindowsOptions, "SecurityContext.WindowsOptions should not be nil")
				assert.Equal(t, "ContainerAdministrator", *container.SecurityContext.WindowsOptions.RunAsUserName, "SecurityContext.WindowsOptions.RunAsUserName should be ContainerAdministrator")
			},
		},
		{
			name: "setting SSL_CERT_FILE should not set security context",
			annotations: map[string]string{
				annotations.KeyEnv: "SSL_CERT_FILE=/tmp/certificates/cert.pem",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Nil(t, container.SecurityContext.WindowsOptions)
			},
		},
	}))

	t.Run("sidecar container should have env vars injected", testCaseFn(testCase{
		annotations: map[string]string{
			annotations.KeyEnv: `HELLO=world, CIAO=mondo, BONJOUR=monde`,
		},
		assertFn: func(t *testing.T, container *corev1.Container) {
			expect := map[string]string{
				"HELLO":                      "world",
				"CIAO":                       "mondo",
				"BONJOUR":                    "monde",
				securityConsts.EnvKeysEnvVar: "HELLO CIAO BONJOUR",
			}

			found := map[string]string{}
			for _, env := range container.Env {
				switch env.Name {
				case "HELLO", "CIAO", "BONJOUR", securityConsts.EnvKeysEnvVar:
					found[env.Name] = env.Value
				}
			}

			assert.Equal(t, expect, found)
		},
	}))

	t.Run("sidecar container should have env vars from secret injected", testSuiteGenerator([]testCase{
		{
			name: "env vars from env and env secret injected",
			annotations: map[string]string{
				annotations.KeyEnv:           `HELLO=world`,
				annotations.KeyEnvFromSecret: `SECRET1=NAME1:SECRETKEY1,SECRET2=NAME2:SECRETKEY2,SECRET3=NAME3`,
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				expect := map[string]string{
					"HELLO":                      "world",
					"SECRET1":                    "NAME1:SECRETKEY1",
					"SECRET2":                    "NAME2:SECRETKEY2",
					"SECRET3":                    "NAME3:NAME3",
					securityConsts.EnvKeysEnvVar: "HELLO SECRET1 SECRET2 SECRET3",
				}

				found := map[string]string{}
				for _, env := range container.Env {
					switch env.Name {
					case "HELLO", "SECRET1", "SECRET2", "SECRET3", securityConsts.EnvKeysEnvVar:
						if env.ValueFrom != nil {
							if env.ValueFrom.SecretKeyRef.Name != "" {
								found[env.Name] = env.ValueFrom.SecretKeyRef.Name + ":" + env.ValueFrom.SecretKeyRef.Key
							} else {
								found[env.Name] = env.ValueFrom.SecretKeyRef.Key
							}
						} else {
							found[env.Name] = env.Value
						}
					}
				}

				assert.Equal(t, expect, found)
			},
		},
		{
			name: "env vars from secret injected trimmed of whitespace",
			annotations: map[string]string{
				annotations.KeyEnvFromSecret: "KEY1=NAME1 ,KEY2=NAME2:SECRETKEY2 ",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.True(t, func() bool {
					envVarsCount := 0
					for _, env := range container.Env {
						if env.Name == "KEY1" && env.ValueFrom.SecretKeyRef.Name == "NAME1" {
							envVarsCount++
						}
						if env.Name == "KEY2" &&
							env.ValueFrom.SecretKeyRef.Name == "NAME2" &&
							env.ValueFrom.SecretKeyRef.Key == "SECRETKEY2" {
							envVarsCount++
						}
					}
					return envVarsCount == 2
				}())
			},
		},
	}))

	t.Run("sidecar container should specify commands only when ignoreEntrypointTolerations match with the pod", func(t *testing.T) {
		testCases := []struct {
			name                        string
			tolerations                 []corev1.Toleration
			ignoreEntrypointTolerations []corev1.Toleration
			explicitCommandSpecified    bool
		}{
			{
				"no tolerations",
				[]corev1.Toleration{},
				[]corev1.Toleration{},
				false,
			},
			{
				"pod contains tolerations from ignoreEntrypointTolerations (single)",
				[]corev1.Toleration{
					{
						Key:    "foo.com/bar",
						Effect: "NoSchedule",
					},
				},
				[]corev1.Toleration{
					{
						Key:    "foo.com/bar",
						Effect: "NoSchedule",
					},
				},
				true,
			},
			{
				"pod contains tolerations from ignoreEntrypointTolerations (multiple)",
				[]corev1.Toleration{
					{
						Key:    "foo.com/bar",
						Effect: "NoSchedule",
					},
					{
						Key:    "foo.com/baz",
						Effect: "NoSchedule",
					},
					{
						Key:    "foo.com/qux",
						Effect: "NoSchedule",
					},
				},
				[]corev1.Toleration{
					{
						Key:    "foo.com/bar",
						Effect: "NoSchedule",
					},
					{
						Key:    "foo.com/baz",
						Effect: "NoSchedule",
					},
				},
				true,
			},
			{
				"pod contains partial tolerations from ignoreEntrypointTolerations",
				[]corev1.Toleration{
					{
						Key:    "foo.com/bar",
						Effect: "NoSchedule",
					},
					{
						Key:    "foo.com/qux",
						Effect: "NoSchedule",
					},
				},
				[]corev1.Toleration{
					{
						Key:    "foo.com/bar",
						Effect: "NoSchedule",
					},
					{
						Key:    "foo.com/baz",
						Effect: "NoSchedule",
					},
				},
				true,
			},
			{
				"pod contains no tolerations from ignoreEntrypointTolerations",
				[]corev1.Toleration{},
				[]corev1.Toleration{
					{
						Key:    "foo.com/baz",
						Effect: "NoSchedule",
					},
				},
				false,
			},
		}
		for _, tc := range testCases {
			c := NewSidecarConfig(&corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: tc.tolerations,
				},
			})
			c.IgnoreEntrypointTolerations = tc.ignoreEntrypointTolerations

			container, err := c.getSidecarContainer(getSidecarContainerOpts{})
			require.NoError(t, err)

			t.Run(tc.name, func(t *testing.T) {
				if tc.explicitCommandSpecified {
					assert.NotEmpty(t, container.Command, "Must contain a command")
					assert.NotEmpty(t, container.Args, "Must contain arguments")
				} else {
					assert.Empty(t, container.Command, "Must not contain a command")
					assert.NotEmpty(t, container.Args, "Must contain arguments")
				}
			})
		}
	})

	t.Run("get sidecar container with appropriate security context", testSuiteGenerator([]testCase{
		{
			name: "set seccomp profile type",
			annotations: map[string]string{
				annotations.KeySidecarSeccompProfileType: "RuntimeDefault",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Nil(t, container.SecurityContext.Capabilities)
				assert.NotNil(t, container.SecurityContext.SeccompProfile, "SecurityContext.SeccompProfile should not be nil")
				assert.Equal(t, corev1.SeccompProfile{Type: corev1.SeccompProfileType("RuntimeDefault")}, *container.SecurityContext.SeccompProfile, "SecurityContext.SeccompProfile.Type should be RuntimeDefault")
			},
		},
		{
			name: "set drop all capabilities",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.SidecarDropALLCapabilities = true
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotNil(t, container.SecurityContext.Capabilities, "SecurityContext.Capabilities should not be nil")
				assert.Equal(t, corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, *container.SecurityContext.Capabilities, "SecurityContext.Capabilities should drop all capabilities")
				assert.Nil(t, container.SecurityContext.SeccompProfile)
			},
		},
		{
			name: "set run as non root explicitly true",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.RunAsNonRoot = true
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotNil(t, container.SecurityContext.RunAsNonRoot, "SecurityContext.RunAsNonRoot should not be nil")
				assert.Equal(t, ptr.Of(true), container.SecurityContext.RunAsNonRoot, "SecurityContext.RunAsNonRoot should be true")
			},
		},
		{
			name: "set run as non root explicitly false",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.RunAsNonRoot = false
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotNil(t, container.SecurityContext.RunAsNonRoot, "SecurityContext.RunAsNonRoot should not be nil")
				assert.Equal(t, ptr.Of(false), container.SecurityContext.RunAsNonRoot, "SecurityContext.RunAsNonRoot should be false")
			},
		},
		{
			name: "set run as user 1000",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.RunAsUser = ptr.Of(int64(1000))
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotNil(t, container.SecurityContext.RunAsUser, "SecurityContext.RunAsUser should not be nil")
				assert.Equal(t, ptr.Of(int64(1000)), container.SecurityContext.RunAsUser, "SecurityContext.RunAsUser should be 1000")
			},
		},
		{
			name: "do not set run as user leave it as default",
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Nil(t, container.SecurityContext.RunAsUser, "SecurityContext.RunAsUser should be nil")
			},
		},
		{
			name: "set run as group 3000",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.RunAsGroup = ptr.Of(int64(3000))
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotNil(t, container.SecurityContext.RunAsGroup, "SecurityContext.RunAsGroup should not be nil")
				assert.Equal(t, ptr.Of(int64(3000)), container.SecurityContext.RunAsGroup, "SecurityContext.RunAsGroup should be 3000")
			},
		},
		{
			name: "do not set run as group leave it as default",
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Nil(t, container.SecurityContext.RunAsGroup, "SecurityContext.RunAsGroup should be nil")
			},
		},
	}))

	t.Run("app health checks", testSuiteGenerator([]testCase{
		{
			name:        "disabled by default",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.NotContains(t, container.Args, "--enable-app-health-check")
				assert.NotContains(t, container.Args, "--app-health-check-path")
				assert.NotContains(t, container.Args, "--app-health-probe-interval")
				assert.NotContains(t, container.Args, "--app-health-probe-timeout")
				assert.NotContains(t, container.Args, "--app-health-threshold")
			},
		},
		{
			name: "enabled with default options",
			annotations: map[string]string{
				annotations.KeyEnableAppHealthCheck: "1",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Contains(t, container.Args, "--enable-app-health-check")

				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--app-health-check-path /healthz")
				assert.Contains(t, args, "--app-health-probe-interval 5")
				assert.Contains(t, args, "--app-health-probe-timeout 500")
				assert.Contains(t, args, "--app-health-threshold 3")
			},
		},
		{
			name: "enabled with custom options",
			annotations: map[string]string{
				annotations.KeyEnableAppHealthCheck:   "1",
				annotations.KeyAppHealthCheckPath:     "/healthcheck",
				annotations.KeyAppHealthProbeInterval: "10",
				annotations.KeyAppHealthProbeTimeout:  "100",
				annotations.KeyAppHealthThreshold:     "2",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				assert.Contains(t, container.Args, "--enable-app-health-check")

				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--app-health-check-path /healthcheck")
				assert.Contains(t, args, "--app-health-probe-interval 10")
				assert.Contains(t, args, "--app-health-probe-timeout 100")
				assert.Contains(t, args, "--app-health-threshold 2")
			},
		},
	}))

	t.Run("sidecar container should have args injected", testCaseFn(testCase{
		annotations: map[string]string{
			annotations.KeyEnableProfiling:    "true",
			annotations.KeyLogLevel:           "debug",
			annotations.KeyHTTPReadBufferSize: "100", // Legacy but still supported
			annotations.KeyReadBufferSize:     "100",
		},
		assertFn: func(t *testing.T, container *corev1.Container) {
			assert.Contains(t, container.Args, "--enable-profiling")
			assert.Contains(t, container.Args, "--log-level")
			assert.Contains(t, container.Args, "debug")
			assert.Contains(t, container.Args, "--dapr-http-read-buffer-size")
			assert.Contains(t, container.Args, "--read-buffer-size")
		},
	}))

	// This validates the current behavior
	// TODO: When app-ssl is deprecated, change this test
	t.Run("app protocol and TLS", testSuiteGenerator([]testCase{
		{
			name:        "default to HTTP protocol",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--app-protocol http")
				assert.NotContains(t, args, "--app-ssl")
			},
		},
		{
			name: "enable app-ssl",
			annotations: map[string]string{
				annotations.KeyAppSSL: "y",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--app-protocol http")
				assert.Contains(t, args, "--app-ssl")
			},
		},
		{
			name: "protocol is grpc with app-ssl",
			annotations: map[string]string{
				annotations.KeyAppProtocol: "grpc",
				annotations.KeyAppSSL:      "y",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--app-protocol grpc")
				assert.Contains(t, args, "--app-ssl")
			},
		},
		{
			name: "protocol is h2c",
			annotations: map[string]string{
				annotations.KeyAppProtocol: "h2c",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--app-protocol h2c")
			},
		},
	}))

	t.Run("app-max-concurrency", testSuiteGenerator([]testCase{
		{
			name:        "not present by default",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--app-max-concurrency")
			},
		},
		{
			name: "set value",
			annotations: map[string]string{
				annotations.KeyAppMaxConcurrency: "10",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--app-max-concurrency 10")
			},
		},
	}))

	t.Run("dapr-http-max-request-size", testSuiteGenerator([]testCase{
		{
			name:        "not present by default",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--dapr-http-max-request-size")
			},
		},
		{
			name: "set value",
			annotations: map[string]string{
				annotations.KeyHTTPMaxRequestSize: "10",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--dapr-http-max-request-size 10")
			},
		},
	}))

	t.Run("max-body-size", testSuiteGenerator([]testCase{
		{
			name:        "not present by default",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--max-body-size")
			},
		},
		{
			name: "set value",
			annotations: map[string]string{
				annotations.KeyMaxBodySize: "1Mi",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--max-body-size 1Mi")
			},
		},
	}))

	t.Run("set resources", testCaseFn(testCase{
		annotations: map[string]string{
			annotations.KeyCPURequest:  "100",
			annotations.KeyMemoryLimit: "100Mi",
		},
		assertFn: func(t *testing.T, container *corev1.Container) {
			assert.Equal(t, "100", container.Resources.Requests.Cpu().String())
			assert.Equal(t, "0", container.Resources.Requests.Memory().String())
			assert.Equal(t, "0", container.Resources.Limits.Cpu().String())
			assert.Equal(t, "100Mi", container.Resources.Limits.Memory().String())
		},
	}))

	t.Run("modes", testSuiteGenerator([]testCase{
		{
			name:                    "default is kubernetes",
			sidecarConfigModifierFn: func(c *SidecarConfig) {},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--mode kubernetes")
			},
		},
		{
			name: "set kubernetes mode explicitly",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.Mode = injectorConsts.ModeKubernetes
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--mode kubernetes")
			},
		},
		{
			name: "set standalone mode explicitly",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.Mode = injectorConsts.ModeStandalone
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--mode standalone")
			},
		},
		{
			name: "omit when value is explicitly empty",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.Mode = ""
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--mode")
			},
		},
		{
			name: "omit when value is invalid",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.Mode = "foo"
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--mode")
			},
		},
	}))

	t.Run("sentry address", testSuiteGenerator([]testCase{
		{
			name: "omitted if empty",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.SentryAddress = ""
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--sentry-address")
			},
		},
		{
			name: "present when set",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.SentryAddress = "somewhere:4000"
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--sentry-address somewhere:4000")
			},
		},
	}))

	t.Run("operator address", testSuiteGenerator([]testCase{
		{
			name: "omitted if empty",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.OperatorAddress = ""
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--control-plane-address")
			},
		},
		{
			name: "present when set",
			sidecarConfigModifierFn: func(c *SidecarConfig) {
				c.OperatorAddress = "somewhere:4000"
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--control-plane-address somewhere:4000")
			},
		},
	}))

	t.Run("jwt audiences", testSuiteGenerator([]testCase{
		{
			name:        "omitted when not set",
			annotations: map[string]string{},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--sentry-request-jwt-audiences")
			},
		},
		{
			name: "present when set with single audience",
			annotations: map[string]string{
				annotations.KeySentryRequestJwtAudiences: "api.example.com",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--sentry-request-jwt-audiences api.example.com")
			},
		},
		{
			name: "present when set with multiple audiences",
			annotations: map[string]string{
				annotations.KeySentryRequestJwtAudiences: "api.example.com,auth.example.com,payments.example.com",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.Contains(t, args, "--sentry-request-jwt-audiences api.example.com,auth.example.com,payments.example.com")
			},
		},
		{
			name: "omitted when annotation is empty",
			annotations: map[string]string{
				annotations.KeySentryRequestJwtAudiences: "",
			},
			assertFn: func(t *testing.T, container *corev1.Container) {
				args := strings.Join(container.Args, " ")
				assert.NotContains(t, args, "--sentry-request-jwt-audiences")
			},
		},
	}))
}

func assertEqualJSON(t *testing.T, val any, expect string) {
	t.Helper()

	actual, err := json.Marshal(val)
	require.NoError(t, err)
	assert.Equal(t, expect, string(actual))
}
