package metrics

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/client"
)

type Metrics struct {
	metrics map[string]float64
}

func New(t *testing.T, ctx context.Context, url string) *Metrics {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	httpclient := client.HTTP(t)
	resp, err := httpclient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Extract the metrics
	parser := expfmt.TextParser{}
	metricFamilies, err := parser.TextToMetricFamilies(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	metrics := make(map[string]float64)
	for _, mf := range metricFamilies {
		for _, m := range mf.GetMetric() {
			metricName := mf.GetName()
			labels := ""
			for _, l := range m.GetLabel() {
				labels += "|" + l.GetName() + ":" + l.GetValue()
			}
			if counter := m.GetCounter(); counter != nil {
				metrics[metricName+labels] = counter.GetValue()
				continue
			}
			if gauge := m.GetGauge(); gauge != nil {
				metrics[metricName+labels] = gauge.GetValue()
				continue
			}
			h := m.GetHistogram()
			if h == nil {
				continue
			}
			for _, b := range h.GetBucket() {
				bucketKey := metricName + "_bucket" + labels + "|le:" + strconv.FormatUint(uint64(b.GetUpperBound()), 10)
				metrics[bucketKey] = float64(b.GetCumulativeCount())
			}
			metrics[metricName+"_count"+labels] = float64(h.GetSampleCount())
			metrics[metricName+"_sum"+labels] = h.GetSampleSum()
		}
	}

	return &Metrics{
		metrics: metrics,
	}
}

func (m *Metrics) GetMetrics() map[string]float64 {
	return m.metrics
}
