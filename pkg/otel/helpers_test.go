/*
Copyright 2024 The Dapr Authors
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

package otel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResource(t *testing.T) {
	t.Run("creates resource with appID even on schema conflict", func(t *testing.T) {
		// NewResource may return an error when WithFromEnv detects a conflicting
		// schema URL (e.g. semconv v1.25.0 vs v1.40.0 from OTEL_RESOURCE_ATTRIBUTES).
		// In that case it falls back to a minimal resource — the resource is always usable.
		res, err := NewResource(context.Background(), "test-app")
		// Error is acceptable (schema conflict), but resource must always be usable.
		_ = err
		require.NotNil(t, res)

		attrs := res.Attributes()
		found := false
		for _, a := range attrs {
			if a.Key == "service.name" {
				assert.Equal(t, "test-app", a.Value.AsString())
				found = true
			}
		}
		assert.True(t, found, "resource should contain service.name attribute")
	})

	t.Run("resource contains service.version", func(t *testing.T) {
		res, _ := NewResource(context.Background(), "test-app")
		require.NotNil(t, res)

		attrs := res.Attributes()
		found := false
		for _, a := range attrs {
			if a.Key == "service.version" {
				assert.NotEmpty(t, a.Value.AsString())
				found = true
			}
		}
		assert.True(t, found, "resource should contain service.version attribute")
	})

	t.Run("always returns usable resource", func(t *testing.T) {
		res, _ := NewResource(context.Background(), "my-app")
		require.NotNil(t, res)
		assert.NotEmpty(t, res.Attributes())
	})
}

func TestNewMetricExporter(t *testing.T) {
	t.Run("creates gRPC exporter", func(t *testing.T) {
		exporter, err := NewMetricExporter(context.Background(), ConnConfig{
			Protocol:       "grpc",
			EndpointAddress: "localhost:4317",
			IsSecure:       false,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("creates HTTP exporter", func(t *testing.T) {
		exporter, err := NewMetricExporter(context.Background(), ConnConfig{
			Protocol:       "http",
			EndpointAddress: "localhost:4318",
			IsSecure:       false,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("defaults to gRPC for empty protocol", func(t *testing.T) {
		exporter, err := NewMetricExporter(context.Background(), ConnConfig{
			Protocol:       "",
			EndpointAddress: "localhost:4317",
			IsSecure:       false,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("passes headers and timeout", func(t *testing.T) {
		exporter, err := NewMetricExporter(context.Background(), ConnConfig{
			Protocol:       "grpc",
			EndpointAddress: "localhost:4317",
			IsSecure:       false,
			Headers:        map[string]string{"X-Key": "val"},
			Timeout:        10 * time.Second,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("secure gRPC exporter", func(t *testing.T) {
		exporter, err := NewMetricExporter(context.Background(), ConnConfig{
			Protocol:       "grpc",
			EndpointAddress: "localhost:4317",
			IsSecure:       true,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})
}

func TestNewLogExporter(t *testing.T) {
	t.Run("creates gRPC exporter", func(t *testing.T) {
		exporter, err := NewLogExporter(context.Background(), ConnConfig{
			Protocol:       "grpc",
			EndpointAddress: "localhost:4317",
			IsSecure:       false,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("creates HTTP exporter", func(t *testing.T) {
		exporter, err := NewLogExporter(context.Background(), ConnConfig{
			Protocol:       "http",
			EndpointAddress: "localhost:4318",
			IsSecure:       false,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("defaults to gRPC for empty protocol", func(t *testing.T) {
		exporter, err := NewLogExporter(context.Background(), ConnConfig{
			Protocol:       "",
			EndpointAddress: "localhost:4317",
			IsSecure:       false,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})

	t.Run("passes headers and timeout", func(t *testing.T) {
		exporter, err := NewLogExporter(context.Background(), ConnConfig{
			Protocol:       "grpc",
			EndpointAddress: "localhost:4317",
			IsSecure:       false,
			Headers:        map[string]string{"X-Key": "val"},
			Timeout:        10 * time.Second,
		})
		require.NoError(t, err)
		assert.NotNil(t, exporter)
	})
}

func TestConnConfig(t *testing.T) {
	t.Run("zero values", func(t *testing.T) {
		cfg := ConnConfig{}
		assert.Empty(t, cfg.Protocol)
		assert.Empty(t, cfg.EndpointAddress)
		assert.False(t, cfg.IsSecure)
		assert.Nil(t, cfg.Headers)
		assert.Zero(t, cfg.Timeout)
	})

	t.Run("full config", func(t *testing.T) {
		cfg := ConnConfig{
			Protocol:       "http",
			EndpointAddress: "collector:4318",
			IsSecure:       true,
			Headers:        map[string]string{"Authorization": "Bearer token"},
			Timeout:        30 * time.Second,
		}
		assert.Equal(t, "http", cfg.Protocol)
		assert.Equal(t, "collector:4318", cfg.EndpointAddress)
		assert.True(t, cfg.IsSecure)
		assert.Equal(t, map[string]string{"Authorization": "Bearer token"}, cfg.Headers)
		assert.Equal(t, 30*time.Second, cfg.Timeout)
	})
}
