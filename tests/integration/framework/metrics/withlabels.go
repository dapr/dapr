/*
Copyright 2025 The Dapr Authors
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
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/client"
)

type MetricsWithLabels struct {
	Metrics map[string]map[string]float64
}

func NewWithLabels(t *testing.T, ctx context.Context, url string) *MetricsWithLabels {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	httpclient := client.HTTP(t)
	resp, err := httpclient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	parser := expfmt.TextParser{}
	metricFamilies, err := parser.TextToMetricFamilies(resp.Body)
	require.NoError(t, err)

	result := &MetricsWithLabels{
		Metrics: make(map[string]map[string]float64),
	}

	for name, mf := range metricFamilies {
		if _, ok := result.Metrics[name]; !ok {
			result.Metrics[name] = make(map[string]float64)
		}

		for _, m := range mf.GetMetric() {
			keys := make([]string, 0, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				keys = append(keys, lp.GetName()+"="+lp.GetValue())
			}
			baseLabelKey := strings.Join(keys, ",")

			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				result.Metrics[name][baseLabelKey] = m.GetCounter().GetValue()

			case dto.MetricType_GAUGE:
				result.Metrics[name][baseLabelKey] = m.GetGauge().GetValue()

			case dto.MetricType_HISTOGRAM:
				hist := m.GetHistogram()
				// Add sum
				sumName := name + "_sum"
				if _, ok := result.Metrics[sumName]; !ok {
					result.Metrics[sumName] = make(map[string]float64)
				}
				result.Metrics[sumName][baseLabelKey] = hist.GetSampleSum()

				// Add count
				countName := name + "_count"
				if _, ok := result.Metrics[countName]; !ok {
					result.Metrics[countName] = make(map[string]float64)
				}
				result.Metrics[countName][baseLabelKey] = float64(hist.GetSampleCount())

				// Add each bucket as its own labeled metric
				bucketName := name + "_bucket"
				if _, ok := result.Metrics[bucketName]; !ok {
					result.Metrics[bucketName] = make(map[string]float64)
				}
				for _, b := range hist.GetBucket() {
					bucketKey := baseLabelKey
					if bucketKey != "" {
						bucketKey += ","
					}
					bucketKey += fmt.Sprintf(`le=%f`, b.GetUpperBound())
					result.Metrics[bucketName][bucketKey] = float64(b.GetCumulativeCount())
				}

			default:
				continue
			}
		}
	}

	return result
}
