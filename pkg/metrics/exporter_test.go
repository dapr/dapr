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

package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/kit/logger"
)

func TestMetricsExporter(t *testing.T) {
	logger := logger.NewLogger("test.logger")

	t.Run("returns default options", func(t *testing.T) {
		e := NewExporter(logger, "test")
		op := e.Options()
		assert.Equal(t, DefaultMetricOptions(), op)
	})

	t.Run("return error if exporter is not initialized", func(t *testing.T) {
		e := &promMetricsExporter{
			&exporter{
				namespace: "test",
				options:   DefaultMetricOptions(),
				logger:    logger,
			},
			nil,
		}
		assert.Error(t, e.startMetricServer())
	})

	t.Run("skip starting metric server", func(t *testing.T) {
		e := NewExporter(logger, "test")
		e.Options().MetricsEnabled = false
		err := e.Init()
		assert.NoError(t, err)
	})
}
