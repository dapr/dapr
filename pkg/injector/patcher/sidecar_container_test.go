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

//nolint:goconst
package patcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
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
			envStr:   "ENV1=value1,ENV2=value2, ENV3=value3",
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
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{})
			c.Env = tc.envStr
			envKeys, envVars := c.getEnv()
			assert.Equal(t, tc.expLen, len(envVars))
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

func TestGetProbeHttpHandler(t *testing.T) {
	pathElements := []string{"api", "v1", "healthz"}
	expectedPath := "/api/v1/healthz"
	expectedHandler := corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: expectedPath,
			Port: intstr.IntOrString{IntVal: 3500},
		},
	}

	assert.EqualValues(t, expectedHandler, getProbeHTTPHandler(3500, pathElements...))
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
		c.SidecarImage = "daprio/dapr"
		c.ImagePullPolicy = "Always"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true
		c.Identity = "pod_identity"

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
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--app-port", "5000",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--placement-host-address", "placement:50000",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Equal(t, 0, len(container.Command))
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// POD_NAME
		assert.Equal(t, "metadata.name", container.Env[1].ValueFrom.FieldRef.FieldPath)
		// DAPR_API_TOKEN
		assert.Equal(t, "secret", container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, "appsecret", container.Env[7].ValueFrom.SecretKeyRef.Name)
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
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--app-port", "5000",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--placement-host-address", "placement:50000",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Equal(t, 0, len(container.Command))
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// POD_NAME
		assert.Equal(t, "metadata.name", container.Env[1].ValueFrom.FieldRef.FieldPath)
		// DAPR_API_TOKEN
		assert.Equal(t, "secret", container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, "appsecret", container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container skipping placement", func(t *testing.T) {
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
		c.PlacementAddress = "" // Set to empty to skip placement
		c.SidecarImage = "daprio/dapr"
		c.ImagePullPolicy = "Always"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true
		c.Identity = "pod_identity"

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
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--app-port", "5000",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Equal(t, 0, len(container.Command))
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, "secret", container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, "appsecret", container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container skipping placement and explicit placement address annotation", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyAppID:                  "app_id",
					annotations.KeyConfig:                 "config",
					annotations.KeyAppPort:                "5000",
					annotations.KeyLogAsJSON:              "true",
					annotations.KeyAPITokenSecret:         "secret",
					annotations.KeyAppTokenSecret:         "appsecret",
					annotations.KeyPlacementHostAddresses: "some-host:50000",
				},
			},
		})
		c.PlacementAddress = "" // Set to empty to skip placement; should be overridden
		c.SidecarImage = "daprio/dapr"
		c.ImagePullPolicy = "Always"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true
		c.Identity = "pod_identity"

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
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--app-port", "5000",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--placement-host-address", "some-host:50000",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Equal(t, 0, len(container.Command))
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, "secret", container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, "appsecret", container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container override listen address", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyConfig:                 "config",
					annotations.KeySidecarListenAddresses: "1.2.3.4,[::1]",
				},
			},
		})

		c.AppID = "app_id"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true

		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{})
		require.NoError(t, err)

		expectedArgs := []string{
			"/daprd",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "1.2.3.4,[::1]",
			"--dapr-public-port", "3501",
			"--app-id", "app_id",
			"--app-protocol", "http",
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--config", "config",
			"--placement-host-address", "placement:50000",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("invalid graceful shutdown seconds", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyGracefulShutdownSeconds: "invalid",
				},
			},
		})

		c.AppID = "app_id"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true

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
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--placement-host-address", "placement:50000",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("valid graceful shutdown seconds", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyGracefulShutdownSeconds: "5",
				},
			},
		})

		c.AppID = "app_id"
		c.Namespace = "dapr-system"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true

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
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "5",
			"--mode", "kubernetes",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--placement-host-address", "placement:50000",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("get sidecar container override image", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeySidecarImage: "override",
				},
			},
		})

		c.AppID = "app_id"
		c.Namespace = "dapr-system"
		c.SidecarImage = "daprio/dapr"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true

		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{})
		require.NoError(t, err)

		assert.Equal(t, "override", container.Image)
	})

	t.Run("get sidecar container without unix domain socket path", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyUnixDomainSocketPath: "",
				},
			},
		})
		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{})
		require.NoError(t, err)

		assert.Equal(t, 0, len(container.VolumeMounts))
	})

	t.Run("get sidecar container with unix domain socket path", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyUnixDomainSocketPath: "/tmp",
				},
			},
		})
		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{
			VolumeMounts: []corev1.VolumeMount{
				{Name: injectorConsts.UnixDomainSocketVolume, MountPath: "/tmp"},
			},
		})
		require.NoError(t, err)

		assert.Len(t, container.VolumeMounts, 1)
		assert.Equal(t, injectorConsts.UnixDomainSocketVolume, container.VolumeMounts[0].Name)
		assert.Equal(t, "/tmp", container.VolumeMounts[0].MountPath)
	})

	t.Run("disable Builtin K8s Secret Store", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyDisableBuiltinK8sSecretStore: "true",
				},
			},
		})

		c.AppID = "app_id"
		c.Namespace = "dapr-system"
		c.SidecarImage = "daprio/dapr"
		c.OperatorAddress = "controlplane:9000"
		c.PlacementAddress = "placement:50000"
		c.SentryAddress = "sentry:50000"
		c.MTLSEnabled = true

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
			"--control-plane-address", "controlplane:9000",
			"--sentry-address", "sentry:50000",
			"--log-level", "info",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--mode", "kubernetes",
			"--enable-metrics",
			"--metrics-port", "9090",
			"--placement-host-address", "placement:50000",
			"--disable-builtin-k8s-secret-store",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("test enable-api-logging", func(t *testing.T) {
		t.Run("unset", func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			})

			c.AppID = "app_id"
			c.Namespace = "dapr-system"
			c.SidecarImage = "daprio/dapr"
			c.OperatorAddress = "controlplane:9000"
			c.PlacementAddress = "placement:50000"
			c.SentryAddress = "sentry:50000"
			c.MTLSEnabled = true

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
				"--control-plane-address", "controlplane:9000",
				"--sentry-address", "sentry:50000",
				"--log-level", "info",
				"--dapr-graceful-shutdown-seconds", "-1",
				"--mode", "kubernetes",
				"--enable-metrics",
				"--metrics-port", "9090",
				"--placement-host-address", "placement:50000",
				"--enable-mtls",
			}

			assert.EqualValues(t, expectedArgs, container.Args)
		})

		t.Run("explicit true", func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyEnableAPILogging: "true",
					},
				},
			})

			c.AppID = "app_id"
			c.Namespace = "dapr-system"
			c.SidecarImage = "daprio/dapr"
			c.OperatorAddress = "controlplane:9000"
			c.PlacementAddress = "placement:50000"
			c.SentryAddress = "sentry:50000"
			c.MTLSEnabled = true

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
				"--control-plane-address", "controlplane:9000",
				"--sentry-address", "sentry:50000",
				"--log-level", "info",
				"--dapr-graceful-shutdown-seconds", "-1",
				"--mode", "kubernetes",
				"--enable-metrics",
				"--metrics-port", "9090",
				"--placement-host-address", "placement:50000",
				"--enable-api-logging=true",
				"--enable-mtls",
			}

			assert.EqualValues(t, expectedArgs, container.Args)
		})

		t.Run("explicit false", func(t *testing.T) {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyEnableAPILogging: "false",
					},
				},
			})

			c.AppID = "app_id"
			c.Namespace = "dapr-system"
			c.SidecarImage = "daprio/dapr"
			c.OperatorAddress = "controlplane:9000"
			c.PlacementAddress = "placement:50000"
			c.SentryAddress = "sentry:50000"
			c.MTLSEnabled = true

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
				"--control-plane-address", "controlplane:9000",
				"--sentry-address", "sentry:50000",
				"--log-level", "info",
				"--dapr-graceful-shutdown-seconds", "-1",
				"--mode", "kubernetes",
				"--enable-metrics",
				"--metrics-port", "9090",
				"--placement-host-address", "placement:50000",
				"--enable-api-logging=false",
				"--enable-mtls",
			}

			assert.EqualValues(t, expectedArgs, container.Args)
		})
	})

	t.Run("sidecar container should have the correct user configured", func(t *testing.T) {
		testCases := []struct {
			envVars string
			isAdmin bool
		}{
			{
				"SSL_CERT_DIR=/tmp/certificates",
				true,
			},
			{
				"SSL_CERT_FILE=/tmp/certificates/cert.pem",
				false,
			},
		}
		for _, tc := range testCases {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						annotations.KeyEnv: tc.envVars,
					},
				},
			})
			c.SetFromPodAnnotations()

			container, err := c.getSidecarContainer(getSidecarContainerOpts{})
			require.NoError(t, err)

			if tc.isAdmin {
				assert.NotNil(t, container.SecurityContext.WindowsOptions, "SecurityContext.WindowsOptions should not be nil")
				assert.Equal(t, "ContainerAdministrator", *container.SecurityContext.WindowsOptions.RunAsUserName, "SecurityContext.WindowsOptions.RunAsUserName should be ContainerAdministrator")
			} else {
				assert.Nil(t, container.SecurityContext.WindowsOptions)
			}
		}
	})

	t.Run("sidecar container should have env vars injected", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyEnv: `HELLO=world, CIAO=mondo, BONJOUR=monde`,
				},
			},
		})
		c.SetFromPodAnnotations()

		container, err := c.getSidecarContainer(getSidecarContainerOpts{})
		require.NoError(t, err)

		expect := map[string]string{
			"HELLO":                  "world",
			"CIAO":                   "mondo",
			"BONJOUR":                "monde",
			authConsts.EnvKeysEnvVar: "HELLO CIAO BONJOUR",
		}

		found := map[string]string{}
		for _, env := range container.Env {
			switch env.Name {
			case "HELLO", "CIAO", "BONJOUR", authConsts.EnvKeysEnvVar:
				found[env.Name] = env.Value
			}
		}

		assert.Equal(t, expect, found)
	})

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
					assert.True(t, len(container.Command) > 0, "Must contain a command")
					assert.True(t, len(container.Args) > 0, "Must contain arguments")
				} else {
					assert.Len(t, container.Command, 0, "Must not contain a command")
					assert.True(t, len(container.Args) > 0, "Must contain arguments")
				}
			})
		}
	})

	t.Run("get sidecar container with appropriate security context", func(t *testing.T) {
		testCases := []struct {
			an               map[string]string
			dropCapabilities bool
		}{
			{
				map[string]string{
					annotations.KeySidecarSeccompProfileType: "RuntimeDefault",
				},
				true,
			},
			{
				map[string]string{},
				false,
			},
		}
		for _, tc := range testCases {
			c := NewSidecarConfig(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tc.an,
				},
			})
			c.SidecarDropALLCapabilities = tc.dropCapabilities
			c.SetFromPodAnnotations()

			container, err := c.getSidecarContainer(getSidecarContainerOpts{})
			require.NoError(t, err)

			if tc.dropCapabilities {
				assert.NotNil(t, container.SecurityContext.Capabilities, "SecurityContext.Capabilities should not be nil")
				assert.Equal(t, corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, *container.SecurityContext.Capabilities, "SecurityContext.Capabilities should drop all capabilities")
			} else {
				assert.Nil(t, container.SecurityContext.Capabilities)
			}

			if len(tc.an) != 0 {
				assert.NotNil(t, container.SecurityContext.SeccompProfile, "SecurityContext.SeccompProfile should not be nil")
				assert.Equal(t, corev1.SeccompProfile{Type: corev1.SeccompProfileType("RuntimeDefault")}, *container.SecurityContext.SeccompProfile, "SecurityContext.SeccompProfile.Type should not be RuntimeDefault")
			} else {
				assert.Nil(t, container.SecurityContext.SeccompProfile)
			}
		}
	})
}
