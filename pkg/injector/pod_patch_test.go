/*
Copyright 2021 The Dapr Authors
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

package injector

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	defaultTestConfig     = "config"
	defaultAPITokenSecret = "secret"
	defaultAppTokenSecret = "appsecret"
)

func TestLogAsJSONEnabled(t *testing.T) {
	t.Run("dapr.io/log-as-json is true", func(t *testing.T) {
		fakeAnnotation := map[string]string{
			daprLogAsJSON: "true",
		}

		assert.Equal(t, true, logAsJSONEnabled(fakeAnnotation))
	})

	t.Run("dapr.io/log-as-json is false", func(t *testing.T) {
		fakeAnnotation := map[string]string{
			daprLogAsJSON: "false",
		}

		assert.Equal(t, false, logAsJSONEnabled(fakeAnnotation))
	})

	t.Run("dapr.io/log-as-json is not given", func(t *testing.T) {
		fakeAnnotation := map[string]string{}

		assert.Equal(t, false, logAsJSONEnabled(fakeAnnotation))
	})
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

func TestGetProbeHttpHandler(t *testing.T) {
	pathElements := []string{"api", "v1", "healthz"}
	expectedPath := "/api/v1/healthz"
	expectedHandler := corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: expectedPath,
			Port: intstr.IntOrString{IntVal: sidecarHTTPPort},
		},
	}

	assert.EqualValues(t, expectedHandler, getProbeHTTPHandler(sidecarHTTPPort, pathElements...))
}

func TestGetSideCarContainer(t *testing.T) {
	t.Run("get sidecar container without debugging", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = defaultTestConfig
		annotations[daprAppPortKey] = "5000"
		annotations[daprLogAsJSON] = "true"
		annotations[daprAPITokenSecret] = defaultAPITokenSecret
		annotations[daprAppTokenSecret] = defaultAppTokenSecret
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always",
			"dapr-system", "controlplane:9000", "placement:50000", nil,
			nil, nil, "", "", "", "sentry:50000", true,
			"pod_identity")

		expectedArgs := []string{
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
			"--enable-api-logging=false",
			"--disable-builtin-k8s-secret-store=false",
			"--log-as-json",
			"--enable-mtls",
		}

		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// POD_NAME
		assert.Equal(t, "metadata.name", container.Env[1].ValueFrom.FieldRef.FieldPath)
		// DAPR_API_TOKEN
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "darpio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with debugging", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = defaultTestConfig
		annotations[daprAppPortKey] = "5000"
		annotations[daprLogAsJSON] = "true"
		annotations[daprAPITokenSecret] = defaultAPITokenSecret
		annotations[daprAppTokenSecret] = defaultAppTokenSecret
		annotations[daprEnableDebugKey] = "true"
		annotations[daprDebugPortKey] = "55555"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always",
			"dapr-system", "controlplane:9000", "placement:50000", nil,
			nil, nil, "", "", "", "sentry:50000", true,
			"pod_identity")

		expectedArgs := []string{
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
			"--enable-api-logging=false",
			"--disable-builtin-k8s-secret-store=false",
			"--log-as-json",
			"--enable-mtls",
		}

		assert.Equal(t, "/dlv", container.Command[0])
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// POD_NAME
		assert.Equal(t, "metadata.name", container.Env[1].ValueFrom.FieldRef.FieldPath)
		// DAPR_API_TOKEN
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with an empty placement addresses", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = defaultTestConfig
		annotations[daprAppPortKey] = "5000"
		annotations[daprLogAsJSON] = "true"
		annotations[daprAPITokenSecret] = defaultAPITokenSecret
		annotations[daprAppTokenSecret] = defaultAppTokenSecret
		annotations[daprEnableDebugKey] = "true"
		annotations[daprPlacementAddressesKey] = ""
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always",
			"dapr-system", "controlplane:9000", "placement:50000",
			nil, nil, nil, "", "", "", "sentry:50000", true,
			"pod_identity")

		expectedArgs := []string{
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
			"--enable-api-logging=false",
			"--disable-builtin-k8s-secret-store=false",
			"--log-as-json",
			"--enable-mtls",
		}

		assert.Equal(t, "/dlv", container.Command[0])
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, defaultAPITokenSecret, container.Env[6].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, defaultAppTokenSecret, container.Env[7].ValueFrom.SecretKeyRef.Name)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container override listen address", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = defaultTestConfig
		annotations[daprListenAddresses] = "1.2.3.4,::1"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always",
			"dapr-system", "controlplane:9000", "placement:50000", nil,
			nil, nil, "", "", "", "sentry:50000", true,
			"pod_identity")

		expectedArgs := []string{
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
			"--enable-api-logging=false",
			"--disable-builtin-k8s-secret-store=false",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("invalid graceful shutdown seconds", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = defaultTestConfig
		annotations[daprGracefulShutdownSeconds] = "invalid"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system",
			"controlplane:9000", "placement:50000", nil, nil, nil, "", "", "", "sentry:50000", true, "pod_identity")

		expectedArgs := []string{
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
			"--enable-api-logging=false",
			"--disable-builtin-k8s-secret-store=false",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("valid graceful shutdown seconds", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = defaultTestConfig
		annotations[daprGracefulShutdownSeconds] = "5"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system",
			"controlplane:9000", "placement:50000", nil, nil, nil, "", "", "", "sentry:50000", true, "pod_identity")

		expectedArgs := []string{
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
			"--enable-api-logging=false",
			"--disable-builtin-k8s-secret-store=false",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("get sidecar container override image", func(t *testing.T) {
		image := "daprio/overvide"
		annotations := map[string]string{
			daprImage: image,
		}

		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system",
			"controlplane:9000", "placement:50000", nil, nil, nil, "", "", "", "sentry:50000", true, "pod_identity")

		assert.Equal(t, image, container.Image)
	})

	t.Run("get sidecar container without unix domain socket path", func(t *testing.T) {
		annotations := map[string]string{
			daprUnixDomainSocketPath: "",
		}

		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system",
			"controlplane:9000", "placement:50000", nil, nil, nil, "", "", "", "sentry:50000", true, "pod_identity")

		assert.Equal(t, 0, len(container.VolumeMounts))
	})

	t.Run("get sidecar container with unix domain socket path", func(t *testing.T) {
		socketPath := "/tmp"
		annotations := map[string]string{
			daprUnixDomainSocketPath: socketPath,
		}

		socketMount := &corev1.VolumeMount{Name: unixDomainSocketVolume, MountPath: socketPath}

		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system",
			"controlplane:9000", "placement:50000", socketMount, nil, nil, "", "", "", "sentry:50000", true, "pod_identity")

		assert.Equal(t, []corev1.VolumeMount{*socketMount}, container.VolumeMounts)
	})

	t.Run("disable Builtin K8s Secret Store", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = defaultTestConfig
		annotations[daprDisableBuiltinK8sSecretStore] = "true"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system", "controlplane:9000", "placement:50000", nil, nil, nil, "", "", "", "sentry:50000", true, "pod_identity")

		expectedArgs := []string{
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
			"--enable-api-logging=false",
			"--disable-builtin-k8s-secret-store=true",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})
}

func TestImagePullPolicy(t *testing.T) {
	testCases := []struct {
		testName       string
		pullPolicy     string
		expectedPolicy corev1.PullPolicy
	}{
		{
			"TestDefaultPullPolicy",
			"",
			corev1.PullIfNotPresent,
		},
		{
			"TestAlwaysPullPolicy",
			"Always",
			corev1.PullAlways,
		},
		{
			"TestNeverPullPolicy",
			"Never",
			corev1.PullNever,
		},
		{
			"TestIfNotPresentPullPolicy",
			"IfNotPresent",
			corev1.PullIfNotPresent,
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			actualPolicy := getPullPolicy(tc.pullPolicy)
			fmt.Println(tc.testName)
			assert.Equal(t, tc.expectedPolicy, actualPolicy)
		})
	}
}

func TestAddDaprEnvVarsToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer corev1.Container
		expOpsLen     int
		expOps        []PatchOperation
	}{
		{
			testName: "empty environment vars",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env",
					Value: []corev1.EnvVar{
						{
							Name:  userContainerDaprHTTPPortName,
							Value: strconv.Itoa(sidecarHTTPPort),
						},
						{
							Name:  userContainerDaprGRPCPortName,
							Value: strconv.Itoa(sidecarAPIGRPCPort),
						},
					},
				},
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
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: corev1.EnvVar{
						Name:  userContainerDaprHTTPPortName,
						Value: strconv.Itoa(sidecarHTTPPort),
					},
				},
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: corev1.EnvVar{
						Name:  userContainerDaprGRPCPortName,
						Value: strconv.Itoa(sidecarAPIGRPCPort),
					},
				},
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
						Name:  userContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: corev1.EnvVar{
						Name:  userContainerDaprHTTPPortName,
						Value: strconv.Itoa(sidecarHTTPPort),
					},
				},
			},
		},
		{
			testName: "multiple existing conflicting env vars",
			mockContainer: corev1.Container{
				Name: "Mock Container",
				Env: []corev1.EnvVar{
					{
						Name:  userContainerDaprHTTPPortName,
						Value: "3510",
					},
					{
						Name:  userContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 0,
			expOps:    []PatchOperation{},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			patchEnv := addDaprEnvVarsToContainers([]corev1.Container{tc.mockContainer})
			fmt.Println(tc.testName)
			assert.Equal(t, tc.expOpsLen, len(patchEnv))
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}

func TestAddSocketVolumeToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer corev1.Container
		socketMount   *corev1.VolumeMount
		expOpsLen     int
		expOps        []PatchOperation
	}{
		{
			testName: "empty var, empty volume",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			socketMount: nil,
			expOpsLen:   0,
			expOps:      []PatchOperation{},
		},
		{
			testName: "existing var, empty volume",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			socketMount: &corev1.VolumeMount{
				Name:      unixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts",
					Value: []corev1.VolumeMount{{
						Name:      unixDomainSocketVolume,
						MountPath: "/tmp",
					}},
				},
			},
		},
		{
			testName: "existing var, existing volume",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{Name: "mock1"},
				},
			},
			socketMount: &corev1.VolumeMount{
				Name:      unixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts/-",
					Value: corev1.VolumeMount{
						Name:      unixDomainSocketVolume,
						MountPath: "/tmp",
					},
				},
			},
		},
		{
			testName: "existing var, multiple existing volumes",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{Name: "mock1"},
					{Name: "mock2"},
				},
			},
			socketMount: &corev1.VolumeMount{
				Name:      unixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts/-",
					Value: corev1.VolumeMount{
						Name:      unixDomainSocketVolume,
						MountPath: "/tmp",
					},
				},
			},
		},
		{
			testName: "existing var, conflict volume name",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{Name: unixDomainSocketVolume},
				},
			},
			socketMount: &corev1.VolumeMount{
				Name:      unixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    []PatchOperation{},
		},
		{
			testName: "existing var, conflict volume mount path",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{MountPath: "/tmp"},
				},
			},
			socketMount: &corev1.VolumeMount{
				Name:      unixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    []PatchOperation{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			patchEnv := addSocketVolumeToContainers([]corev1.Container{tc.mockContainer}, tc.socketMount)
			assert.Equal(t, tc.expOpsLen, len(patchEnv))
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
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
			map[string]string{daprUnixDomainSocketPath: ""},
			nil,
			nil,
			nil,
		},
		{
			"append on empty volumes",
			map[string]string{daprUnixDomainSocketPath: "/tmp"},
			nil,
			[]corev1.Volume{{
				Name: unixDomainSocketVolume,
			}},
			&corev1.VolumeMount{Name: unixDomainSocketVolume, MountPath: "/tmp"},
		},
		{
			"append on existed volumes",
			map[string]string{daprUnixDomainSocketPath: "/tmp"},
			[]corev1.Volume{
				{Name: "mock"},
			},
			[]corev1.Volume{{
				Name: unixDomainSocketVolume,
			}, {
				Name: "mock",
			}},
			&corev1.VolumeMount{Name: unixDomainSocketVolume, MountPath: "/tmp"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := corev1.Pod{}
			pod.Annotations = tc.annotations
			pod.Spec.Volumes = tc.originalVolumes

			socketMount := appendUnixDomainSocketVolume(&pod)

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

func TestPodContainsVolume(t *testing.T) {
	testCases := []struct {
		testName   string
		podVolumes []corev1.Volume
		volumeName string
		expect     bool
	}{
		{
			"pod with no volumes",
			[]corev1.Volume{},
			"volume1",
			false,
		},
		{
			"pod does not contain volume",
			[]corev1.Volume{
				{Name: "volume"},
			},
			"volume1",
			false,
		},
		{
			"pod contains volume",
			[]corev1.Volume{
				{Name: "volume1"},
				{Name: "volume2"},
			},
			"volume2",
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := corev1.Pod{
				Spec: corev1.PodSpec{
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
		expVolumeMounts           []corev1.VolumeMount
	}{
		{
			"no annotations",
			"",
			"",
			[]string{"mount1", "mount2"},
			[]corev1.VolumeMount{},
		},
		{
			"annotations with volumes present in pod",
			"mount1:/tmp/mount1,mount2:/tmp/mount2",
			"mount3:/tmp/mount3,mount4:/tmp/mount4",
			[]string{"mount1", "mount2", "mount3", "mount4"},
			[]corev1.VolumeMount{
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
			[]corev1.VolumeMount{
				{Name: "mount1", MountPath: "/tmp/mount1", ReadOnly: true},
				{Name: "mount2", MountPath: "/tmp/mount2", ReadOnly: true},
				{Name: "mount4", MountPath: "/tmp/mount4", ReadOnly: false},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := corev1.Pod{}
			pod.Annotations = map[string]string{
				daprVolumeMountsReadOnlyKey:  tc.volumeReadOnlyAnnotation,
				daprVolumeMountsReadWriteKey: tc.volumeReadWriteAnnotation,
			}
			pod.Spec.Volumes = []corev1.Volume{}
			for _, volumeName := range tc.podVolumeMountNames {
				pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: volumeName})
			}

			volumeMounts := getVolumeMounts(pod)
			assert.Equal(t, tc.expVolumeMounts, volumeMounts)
		})
	}
}
