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

//nolint:goconst
package sidecar

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/dapr/dapr/pkg/injector/annotations"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
)

const (
	defaultTestConfig     = "config"
	defaultAPITokenSecret = "secret"
	defaultAppTokenSecret = "appsecret"
)

func TestGetResourceRequirements(t *testing.T) {
	t.Run("no resource requirements", func(t *testing.T) {
		r, err := getResourceRequirements(nil)
		assert.Nil(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource limits", func(t *testing.T) {
		a := map[string]string{annotations.KeyCPULimit: "100m", annotations.KeyMemoryLimit: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.Nil(t, err)
		assert.Equal(t, "100m", r.Limits.Cpu().String())
		assert.Equal(t, "1Gi", r.Limits.Memory().String())
	})

	t.Run("invalid cpu limit", func(t *testing.T) {
		a := map[string]string{annotations.KeyCPULimit: "cpu", annotations.KeyMemoryLimit: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory limit", func(t *testing.T) {
		a := map[string]string{annotations.KeyCPULimit: "100m", annotations.KeyMemoryLimit: "memory"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource requests", func(t *testing.T) {
		a := map[string]string{annotations.KeyCPURequest: "100m", annotations.KeyMemoryRequest: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.Nil(t, err)
		assert.Equal(t, "100m", r.Requests.Cpu().String())
		assert.Equal(t, "1Gi", r.Requests.Memory().String())
	})

	t.Run("invalid cpu request", func(t *testing.T) {
		a := map[string]string{annotations.KeyCPURequest: "cpu", annotations.KeyMemoryRequest: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory request", func(t *testing.T) {
		a := map[string]string{annotations.KeyCPURequest: "100m", annotations.KeyMemoryRequest: "memory"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})
}

func TestGetProbeHttpHandler(t *testing.T) {
	pathElements := []string{"api", "v1", "healthz"}
	expectedPath := "/api/v1/healthz"
	expectedHandler := corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: expectedPath,
			Port: intstr.IntOrString{IntVal: SidecarHTTPPort},
		},
	}

	assert.EqualValues(t, expectedHandler, getProbeHTTPHandler(SidecarHTTPPort, pathElements...))
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
	t.Run("get sidecar container without debugging", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyAppPort] = "5000"
		an[annotations.KeyLogAsJSON] = "true"
		an[annotations.KeyAPITokenSecret] = defaultAPITokenSecret
		an[annotations.KeyAppTokenSecret] = defaultAppTokenSecret

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			DaprSidecarImage:        "daprio/dapr",
			ImagePullPolicy:         "Always",
			Namespace:               "dapr-system",
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
			Identity:                "pod_identity",
		})

		expectedArgs := []string{
			"/daprd",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "5000",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "placement:50000",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=false",
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
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with debugging", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyAppPort] = "5000"
		an[annotations.KeyLogAsJSON] = "true"
		an[annotations.KeyAPITokenSecret] = defaultAPITokenSecret
		an[annotations.KeyAppTokenSecret] = defaultAppTokenSecret
		an[annotations.KeyEnableDebug] = "true"
		an[annotations.KeyDebugPort] = "55555"

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			DaprSidecarImage:        "daprio/dapr",
			ImagePullPolicy:         "Always",
			Namespace:               "dapr-system",
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
			Identity:                "pod_identity",
		})

		expectedArgs := []string{
			"/dlv",
			"--listen=:55555",
			"--accept-multiclient",
			"--headless=true",
			"--log",
			"--api-version=2",
			"exec",
			"/daprd",
			"--",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "5000",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "placement:50000",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=false",
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
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with an empty placement addresses", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyAppPort] = "5000"
		an[annotations.KeyLogAsJSON] = "true"
		an[annotations.KeyAPITokenSecret] = defaultAPITokenSecret
		an[annotations.KeyAppTokenSecret] = defaultAppTokenSecret
		an[annotations.KeyEnableDebug] = "true"
		an[annotations.KeyPlacementHostAddresses] = ""

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			DaprSidecarImage:        "daprio/dapr",
			ImagePullPolicy:         "Always",
			Namespace:               "dapr-system",
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
			Identity:                "pod_identity",
		})

		expectedArgs := []string{
			"/dlv",
			"--listen=:40000",
			"--accept-multiclient",
			"--headless=true",
			"--log",
			"--api-version=2",
			"exec",
			"/daprd",
			"--",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "5000",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=false",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Equal(t, 0, len(container.Command))
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with SkipPlacement=true", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyAppPort] = "5000"
		an[annotations.KeyLogAsJSON] = "true"
		an[annotations.KeyAPITokenSecret] = defaultAPITokenSecret
		an[annotations.KeyAppTokenSecret] = defaultAppTokenSecret
		an[annotations.KeyEnableDebug] = "true"

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			DaprSidecarImage:        "daprio/dapr",
			ImagePullPolicy:         "Always",
			Namespace:               "dapr-system",
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
			Identity:                "pod_identity",
			SkipPlacement:           true,
		})

		expectedArgs := []string{
			"/dlv",
			"--listen=:40000",
			"--accept-multiclient",
			"--headless=true",
			"--log",
			"--api-version=2",
			"exec",
			"/daprd",
			"--",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "5000",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=false",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Equal(t, 0, len(container.Command))
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with SkipPlacement=true and explicit placement address annotation", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyAppPort] = "5000"
		an[annotations.KeyLogAsJSON] = "true"
		an[annotations.KeyAPITokenSecret] = defaultAPITokenSecret
		an[annotations.KeyAppTokenSecret] = defaultAppTokenSecret
		an[annotations.KeyEnableDebug] = "true"
		an[annotations.KeyPlacementHostAddresses] = "some-host:50000"

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			DaprSidecarImage:        "daprio/dapr",
			ImagePullPolicy:         "Always",
			Namespace:               "dapr-system",
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
			Identity:                "pod_identity",
			SkipPlacement:           true,
		})

		expectedArgs := []string{
			"/dlv",
			"--listen=:40000",
			"--accept-multiclient",
			"--headless=true",
			"--log",
			"--api-version=2",
			"exec",
			"/daprd",
			"--",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "5000",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "some-host:50000",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=false",
			"--log-as-json",
			"--enable-mtls",
		}

		// Command should be empty, image's entrypoint to be used.
		assert.Equal(t, 0, len(container.Command))
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "daprio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container override listen address", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeySidecarListenAddresses] = "1.2.3.4,::1"

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
		})

		expectedArgs := []string{
			"/daprd",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "1.2.3.4,::1",
			"--dapr-public-port", "3501",
			"--app-port", "",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "placement:50000",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=false",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("invalid graceful shutdown seconds", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyGracefulShutdownSeconds] = "invalid"

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
		})

		expectedArgs := []string{
			"/daprd",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "placement:50000",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=false",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("valid graceful shutdown seconds", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyGracefulShutdownSeconds] = "5"

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
		})

		expectedArgs := []string{
			"/daprd",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "placement:50000",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "5",
			"--disable-builtin-k8s-secret-store=false",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("get sidecar container override image", func(t *testing.T) {
		image := "daprio/override"
		an := map[string]string{
			annotations.KeySidecarImage: image,
		}

		container, _ := GetSidecarContainer(ContainerConfig{
			Annotations:      an,
			DaprSidecarImage: "daprio/dapr",
		})

		assert.Equal(t, image, container.Image)
	})

	t.Run("get sidecar container without unix domain socket path", func(t *testing.T) {
		an := map[string]string{
			annotations.KeyUnixDomainSocketPath: "",
		}

		container, _ := GetSidecarContainer(ContainerConfig{
			Annotations: an,
		})

		assert.Equal(t, 0, len(container.VolumeMounts))
	})

	t.Run("get sidecar container with unix domain socket path", func(t *testing.T) {
		socketPath := "/tmp"
		an := map[string]string{
			annotations.KeyUnixDomainSocketPath: socketPath,
		}

		container, _ := GetSidecarContainer(ContainerConfig{
			Annotations: an,
			VolumeMounts: []corev1.VolumeMount{
				{Name: UnixDomainSocketVolume, MountPath: socketPath},
			},
		})

		assert.Len(t, container.VolumeMounts, 1)
		assert.Equal(t, UnixDomainSocketVolume, container.VolumeMounts[0].Name)
		assert.Equal(t, socketPath, container.VolumeMounts[0].MountPath)
	})

	t.Run("disable Builtin K8s Secret Store", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyDisableBuiltinK8sSecretStore] = "true"

		container, _ := GetSidecarContainer(ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
		})

		expectedArgs := []string{
			"/daprd",
			"--mode", "kubernetes",
			"--dapr-http-port", "3500",
			"--dapr-grpc-port", "50001",
			"--dapr-internal-grpc-port", "50002",
			"--dapr-listen-addresses", "[::1],127.0.0.1",
			"--dapr-public-port", "3501",
			"--app-port", "",
			"--app-id", "app_id",
			"--control-plane-address", "controlplane:9000",
			"--app-protocol", "http",
			"--placement-host-address", "placement:50000",
			"--config", defaultTestConfig,
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--dapr-graceful-shutdown-seconds", "-1",
			"--disable-builtin-k8s-secret-store=true",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("test enable-api-logging", func(t *testing.T) {
		an := map[string]string{}
		an[annotations.KeyConfig] = defaultTestConfig
		an[annotations.KeyDisableBuiltinK8sSecretStore] = "true"

		containerConfig := ContainerConfig{
			AppID:                   "app_id",
			Annotations:             an,
			ControlPlaneAddress:     "controlplane:9000",
			PlacementServiceAddress: "placement:50000",
			SentryAddress:           "sentry:50000",
			MTLSEnabled:             true,
		}

		t.Run("unset", func(t *testing.T) {
			container, _ := GetSidecarContainer(containerConfig)

			expectedArgs := []string{
				"/daprd",
				"--mode", "kubernetes",
				"--dapr-http-port", "3500",
				"--dapr-grpc-port", "50001",
				"--dapr-internal-grpc-port", "50002",
				"--dapr-listen-addresses", "[::1],127.0.0.1",
				"--dapr-public-port", "3501",
				"--app-port", "",
				"--app-id", "app_id",
				"--control-plane-address", "controlplane:9000",
				"--app-protocol", "http",
				"--placement-host-address", "placement:50000",
				"--config", defaultTestConfig,
				"--log-level", "info",
				"--app-max-concurrency", "-1",
				"--sentry-address", "sentry:50000",
				"--enable-metrics=true",
				"--metrics-port", "9090",
				"--dapr-http-max-request-size", "-1",
				"--dapr-http-read-buffer-size", "-1",
				"--dapr-graceful-shutdown-seconds", "-1",
				"--disable-builtin-k8s-secret-store=true",
				"--enable-mtls",
			}

			assert.EqualValues(t, expectedArgs, container.Args)
		})

		t.Run("explicit true", func(t *testing.T) {
			an[annotations.KeyEnableAPILogging] = "true"

			container, _ := GetSidecarContainer(containerConfig)

			expectedArgs := []string{
				"/daprd",
				"--mode", "kubernetes",
				"--dapr-http-port", "3500",
				"--dapr-grpc-port", "50001",
				"--dapr-internal-grpc-port", "50002",
				"--dapr-listen-addresses", "[::1],127.0.0.1",
				"--dapr-public-port", "3501",
				"--app-port", "",
				"--app-id", "app_id",
				"--control-plane-address", "controlplane:9000",
				"--app-protocol", "http",
				"--placement-host-address", "placement:50000",
				"--config", defaultTestConfig,
				"--log-level", "info",
				"--app-max-concurrency", "-1",
				"--sentry-address", "sentry:50000",
				"--enable-metrics=true",
				"--metrics-port", "9090",
				"--dapr-http-max-request-size", "-1",
				"--dapr-http-read-buffer-size", "-1",
				"--dapr-graceful-shutdown-seconds", "-1",
				"--disable-builtin-k8s-secret-store=true",
				"--enable-api-logging=true",
				"--enable-mtls",
			}

			assert.EqualValues(t, expectedArgs, container.Args)
		})

		t.Run("explicit false", func(t *testing.T) {
			an[annotations.KeyEnableAPILogging] = "false"

			container, _ := GetSidecarContainer(containerConfig)

			expectedArgs := []string{
				"/daprd",
				"--mode", "kubernetes",
				"--dapr-http-port", "3500",
				"--dapr-grpc-port", "50001",
				"--dapr-internal-grpc-port", "50002",
				"--dapr-listen-addresses", "[::1],127.0.0.1",
				"--dapr-public-port", "3501",
				"--app-port", "",
				"--app-id", "app_id",
				"--control-plane-address", "controlplane:9000",
				"--app-protocol", "http",
				"--placement-host-address", "placement:50000",
				"--config", defaultTestConfig,
				"--log-level", "info",
				"--app-max-concurrency", "-1",
				"--sentry-address", "sentry:50000",
				"--enable-metrics=true",
				"--metrics-port", "9090",
				"--dapr-http-max-request-size", "-1",
				"--dapr-http-read-buffer-size", "-1",
				"--dapr-graceful-shutdown-seconds", "-1",
				"--disable-builtin-k8s-secret-store=true",
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
			an := map[string]string{}
			an[annotations.KeyEnv] = tc.envVars

			container, _ := GetSidecarContainer(ContainerConfig{
				Annotations: an,
			})

			if tc.isAdmin {
				assert.NotNil(t, container.SecurityContext.WindowsOptions, "SecurityContext.WindowsOptions should not be nil")
				assert.Equal(t, "ContainerAdministrator", *container.SecurityContext.WindowsOptions.RunAsUserName, "SecurityContext.WindowsOptions.RunAsUserName should be ContainerAdministrator")
			} else {
				assert.Nil(t, container.SecurityContext.WindowsOptions)
			}
		}
	})

	t.Run("sidecar container should have env vars injected", func(t *testing.T) {
		an := map[string]string{
			annotations.KeyEnv: `HELLO=world, CIAO=mondo, BONJOUR=monde`,
		}
		container, _ := GetSidecarContainer(ContainerConfig{
			Annotations: an,
		})

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
			container, _ := GetSidecarContainer(ContainerConfig{
				Tolerations:                 tc.tolerations,
				IgnoreEntrypointTolerations: tc.ignoreEntrypointTolerations,
			})

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
			container, _ := GetSidecarContainer(ContainerConfig{
				Annotations:                tc.an,
				SidecarDropALLCapabilities: tc.dropCapabilities,
			})

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

func TestAppendUnixDomainSocketVolume(t *testing.T) {
	testCases := []struct {
		testName        string
		annotations     map[string]string
		originalVolumes []corev1.Volume
		expectVolumes   []corev1.Volume
		exportMount     *corev1.VolumeMount
	}{
		{
			"empty value",
			map[string]string{annotations.KeyUnixDomainSocketPath: ""},
			nil,
			nil,
			nil,
		},
		{
			"append on empty volumes",
			map[string]string{annotations.KeyUnixDomainSocketPath: "/tmp"},
			nil,
			[]corev1.Volume{{
				Name: UnixDomainSocketVolume,
			}},
			&corev1.VolumeMount{Name: UnixDomainSocketVolume, MountPath: "/tmp"},
		},
		{
			"append on existed volumes",
			map[string]string{annotations.KeyUnixDomainSocketPath: "/tmp"},
			[]corev1.Volume{
				{Name: "mock"},
			},
			[]corev1.Volume{{
				Name: UnixDomainSocketVolume,
			}, {
				Name: "mock",
			}},
			&corev1.VolumeMount{Name: UnixDomainSocketVolume, MountPath: "/tmp"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := corev1.Pod{}
			pod.Annotations = tc.annotations
			pod.Spec.Volumes = tc.originalVolumes

			socketMount := GetUnixDomainSocketVolumeMount(&pod)

			if tc.exportMount == nil {
				assert.Equal(t, tc.exportMount, socketMount)
			} else {
				assert.Equal(t, tc.exportMount.Name, socketMount.Name)
				assert.Equal(t, tc.exportMount.MountPath, socketMount.MountPath)
			}

			assert.Equal(t, len(tc.expectVolumes), len(pod.Spec.Volumes))
		})
	}
}
