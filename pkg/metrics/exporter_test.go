// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/kit/logger"
)

func TestMetricsExporter(t *testing.T) {
	t.Run("returns default options", func(t *testing.T) {
		e := NewExporter("test")
		op := e.Options()
		assert.Equal(t, defaultMetricOptions(), op)
	})

	t.Run("return error if exporter is not initialized", func(t *testing.T) {
		e := &promMetricsExporter{
			&exporter{
				namespace: "test",
				options:   defaultMetricOptions(),
				logger:    logger.NewLogger("dapr.metrics"),
			},
			nil,
		}
		assert.Error(t, e.startMetricServer())
	})

	t.Run("skip starting metric server", func(t *testing.T) {
		e := NewExporter("test")
		e.Options().MetricsEnabled = false
		err := e.Init()
		assert.NoError(t, err)
	})
}
