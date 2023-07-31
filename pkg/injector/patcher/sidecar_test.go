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

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
)

func TestSidecarConfigInit(t *testing.T) {
	c := NewSidecarConfig(&corev1.Pod{})

	// Ensure default values are set (and that those without a default value are zero)
	// Check properties of supported kinds: bools, strings, ints
	assert.Equal(t, "", c.Config)
	assert.Equal(t, "info", c.LogLevel)
	assert.Equal(t, int32(0), c.AppPort)
	assert.Equal(t, int32(9090), c.SidecarMetricsPort)
	assert.False(t, c.EnableProfiling)
	assert.True(t, c.EnableMetrics)

	// These properties don't have an annotation but should have a default value anyways
	assert.Equal(t, injectorConsts.ModeKubernetes, c.Mode)
	assert.Equal(t, int32(3500), c.SidecarHTTPPort)
	assert.Equal(t, int32(50001), c.SidecarAPIGRPCPort)

	// Nullable properties
	assert.Nil(t, c.EnableAPILogging)
}

func TestSidecarConfigSetFromAnnotations(t *testing.T) {
	t.Run("set properties", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{})

		// Set properties of supported kinds: bools, strings, ints
		c.setFromAnnotations(map[string]string{
			annotations.KeyEnabled:          "1", // Will be cast using utils.IsTruthy
			annotations.KeyAppID:            "myappid",
			annotations.KeyAppPort:          "9876",
			annotations.KeyMetricsPort:      "6789",  // Override default value
			annotations.KeyEnableAPILogging: "false", // Nullable property
		})

		assert.True(t, c.Enabled)
		assert.Equal(t, "myappid", c.AppID)
		assert.Equal(t, int32(9876), c.AppPort)
		assert.Equal(t, int32(6789), c.SidecarMetricsPort)

		// Nullable properties
		_ = assert.NotNil(t, c.EnableAPILogging) &&
			assert.False(t, *c.EnableAPILogging)

		// Should maintain default values
		assert.Equal(t, "info", c.LogLevel)
	})

	t.Run("skip invalid properties", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{})

		// Set properties of supported kinds: bools, strings, ints
		c.setFromAnnotations(map[string]string{
			annotations.KeyAppPort:            "zorro",
			annotations.KeyHTTPMaxRequestSize: "batman", // Nullable property
		})

		assert.Equal(t, int32(0), c.AppPort)
		assert.Nil(t, c.HTTPMaxRequestSize)
	})
}
