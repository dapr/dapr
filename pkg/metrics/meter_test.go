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

package metrics

import (
	"testing"

	ocprom "contrib.go.opencensus.io/exporter/prometheus"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
)

func TestMeterStop(t *testing.T) {
	measure := stats.Int64("meter_test/count", "meter test", stats.UnitDimensionless)

	newStarted := func(t *testing.T) *Meter {
		t.Helper()
		m := NewMeter()
		t.Cleanup(m.Stop)
		m.Start()
		require.NoError(t, m.Register(&view.View{
			Name:        "meter_test_count",
			Measure:     measure,
			Aggregation: view.Count(),
		}))
		m.Record(nil, []stats.Measurement{measure.M(1)}, nil)
		_, err := m.RetrieveData("meter_test_count")
		require.NoError(t, err)
		return m
	}

	// gather reports whether a scrape sees the same series exported twice,
	// which is what a meter outliving its runtime causes.
	gather := func(t *testing.T) error {
		t.Helper()
		reg := prom.NewRegistry()
		_, err := ocprom.NewExporter(ocprom.Options{Namespace: "dapr", Registry: reg})
		require.NoError(t, err)
		_, err = (prom.Gatherers{reg, prom.DefaultGatherer}).Gather()
		return err
	}

	first := newStarted(t)
	require.NoError(t, gather(t))

	second := newStarted(t)
	require.ErrorContains(t, gather(t), "was collected before")

	first.Stop()
	require.NoError(t, gather(t))

	assert.NotPanics(t, func() {
		first.Record(nil, []stats.Measurement{measure.M(1)}, nil)
		first.Stop()
	})

	second.Stop()
	require.NoError(t, gather(t))

	assert.NotPanics(t, NewMeter().Stop)
}
