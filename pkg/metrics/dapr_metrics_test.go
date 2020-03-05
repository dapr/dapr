// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetricsExporter(t *testing.T) {
	t.Run("returns default options", func(t *testing.T) {
		e := NewDaprMetricExporter()
		op := e.Options()
		assert.Equal(t, defaultMetricOptions(), op)
	})

	t.Run("return error if exporter is not initialized", func(t *testing.T) {
		e := NewDaprMetricExporter()
		assert.Error(t, e.StartMetricServer())
	})

	t.Run("return error if exporter is not initialized", func(t *testing.T) {
		e := NewDaprMetricExporter()
		assert.Error(t, e.StartMetricServer())
	})

	t.Run("skip starting metric server", func(t *testing.T) {
		e := NewDaprMetricExporter()
		e.Options().MetricsEnabled = false
		e.Init("test")
		assert.Equal(t, "test", e.namespace)
		assert.Nil(t, e.exporter)
		assert.NoError(t, e.StartMetricServer())
	})
}
