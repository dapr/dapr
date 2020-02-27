// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package injector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLogAsJSON(t *testing.T) {
	t.Run("dapr.io/log-as-json is true", func(t *testing.T) {
		var fakeAnnotation = map[string]string{
			daprLogAsJSON: "true",
		}

		assert.Equal(t, "true", getLogAsJSON(fakeAnnotation))
	})

	t.Run("dapr.io/log-as-json is false", func(t *testing.T) {
		var fakeAnnotation = map[string]string{
			daprLogAsJSON: "false",
		}

		assert.Equal(t, "false", getLogAsJSON(fakeAnnotation))
	})

	t.Run("dapr.io/log-as-json is not given", func(t *testing.T) {
		var fakeAnnotation = map[string]string{}

		assert.Equal(t, "false", getLogAsJSON(fakeAnnotation))
	})
}

func TestGetSideCarContainer(t *testing.T) {
	container := getSidecarContainer("5000", "http", "app_id", "config", "darpio/dapr", "dapr-system", "controlplane:9000", "placement:50000", "false", "info", "true", "-1", nil, "", "sentry:50000", true, "pod_identity")

	var expectedArgs = []string{
		"--mode", "kubernetes",
		"--dapr-http-port", "3500",
		"--dapr-grpc-port", "50001",
		"--app-port", "5000",
		"--app-id", "app_id",
		"--control-plane-address", "controlplane:9000",
		"--protocol", "http",
		"--placement-address", "placement:50000",
		"--config", "config",
		"--enable-profiling", "false",
		"--log-level", "info",
		"--log-as-json", "true",
		"--max-concurrency", "-1",
		"--sentry-address", "sentry:50000",
	}

	assert.EqualValues(t, expectedArgs, container.Args)
}
