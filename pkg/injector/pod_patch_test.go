// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package injector

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestLogAsJSONEnabled(t *testing.T) {
	t.Run("dapr.io/log-as-json is true", func(t *testing.T) {
		fakeAnnotation := map[string]string{
			daprLogAsJSON: trueString,
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
	expectedHandler := corev1.Handler{
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
		annotations[daprConfigKey] = "config"
		annotations[daprAppPortKey] = "5000"
		annotations[daprLogAsJSON] = trueString
		annotations[daprAPITokenSecret] = "secret"
		annotations[daprAppTokenSecret] = "appsecret"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system", "controlplane:9000", "placement:50000", nil, "", "", "", "sentry:50000", true, "pod_identity")

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
			"--config", "config",
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--log-as-json",
			"--enable-mtls",
		}

		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, "secret", container.Env[5].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, "appsecret", container.Env[6].ValueFrom.SecretKeyRef.Name)
		// default image
		assert.Equal(t, "darpio/dapr", container.Image)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})

	t.Run("get sidecar container with debugging", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = "config"
		annotations[daprAppPortKey] = "5000"
		annotations[daprLogAsJSON] = trueString
		annotations[daprAPITokenSecret] = "secret"
		annotations[daprAppTokenSecret] = "appsecret"
		annotations[daprEnableDebugKey] = trueString
		annotations[daprDebugPortKey] = "55555"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system", "controlplane:9000", "placement:50000", nil, "", "", "", "sentry:50000", true, "pod_identity")

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
			"--config", "config",
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--log-as-json",
			"--enable-mtls",
		}

		assert.Equal(t, "/dlv", container.Command[0])
		// NAMESPACE
		assert.Equal(t, "dapr-system", container.Env[0].Value)
		// DAPR_API_TOKEN
		assert.Equal(t, "secret", container.Env[5].ValueFrom.SecretKeyRef.Name)
		// DAPR_APP_TOKEN
		assert.Equal(t, "appsecret", container.Env[6].ValueFrom.SecretKeyRef.Name)
		assert.EqualValues(t, expectedArgs, container.Args)
		assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	})
	t.Run("get sidecar container override listen address", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = "config"
		annotations[daprListenAddresses] = "1.2.3.4,::1"
		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system", "controlplane:9000", "placement:50000", nil, "", "", "", "sentry:50000", true, "pod_identity")

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
			"--config", "config",
			"--log-level", "info",
			"--app-max-concurrency", "-1",
			"--sentry-address", "sentry:50000",
			"--enable-metrics=true",
			"--metrics-port", "9090",
			"--dapr-http-max-request-size", "-1",
			"--dapr-http-read-buffer-size", "-1",
			"--enable-mtls",
		}

		assert.EqualValues(t, expectedArgs, container.Args)
	})

	t.Run("get sidecar container override image", func(t *testing.T) {
		image := "daprio/overvide"
		annotations := map[string]string{
			daprImage: image,
		}

		container, _ := getSidecarContainer(annotations, "app_id", "darpio/dapr", "Always", "dapr-system", "controlplane:9000", "placement:50000", nil, "", "", "", "sentry:50000", true, "pod_identity")

		assert.Equal(t, image, container.Image)
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
