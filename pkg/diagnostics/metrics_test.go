/*
Copyright 2026 The Dapr Authors
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

package diagnostics

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"

	"github.com/dapr/dapr/pkg/config"
)

// TestInitMetricsOnTwoMeters initialises metrics on a second meter while the
// first is still recording, as a process running more than one runtime does.
// Registering a view canonicalizes its Aggregation in place, so an Aggregation
// shared by both meters is a data race under -race.
func TestInitMetricsOnTwoMeters(t *testing.T) {
	first := view.NewMeter()
	t.Cleanup(first.Stop)
	require.NoError(t, InitMetrics(first, "first", "default", config.MetricSpec{}))

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			stats.RecordWithOptions(ctx,
				stats.WithRecorder(first),
				stats.WithMeasurements(
					DefaultGRPCMonitoring.serverReceivedBytes.M(1<<10),
					DefaultWorkflowMonitoring.workflowPayloadSizeRatio.M(0.5),
				),
			)
		}
	}()

	second := view.NewMeter()
	t.Cleanup(second.Stop)
	require.NoError(t, InitMetrics(second, "second", "default", config.MetricSpec{}))
	cancel()
	wg.Wait()

	for _, name := range []string{
		"grpc.io/server/received_bytes_per_rpc",
		"runtime/workflow/payload/size_ratio",
	} {
		v1, v2 := first.Find(name), second.Find(name)
		require.NotNil(t, v1, name)
		require.NotNil(t, v2, name)
		assert.NotSame(t, v1.Aggregation, v2.Aggregation, name)
	}
}
