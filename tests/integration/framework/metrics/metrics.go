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
	"net/http"
	"strconv"
	"strings"

	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework/client"
)

type Metric struct {
	Value float64
	Name  string
}

type Metrics struct {
	metrics map[string]float64
}

func New(t assert.TestingT, ctx context.Context, url string) *Metrics {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	assert.NoError(t, err) //nolint:testifylint

	httpclient := client.HTTP(t)
	resp, err := httpclient.Do(req)
	if !assert.NoError(t, err) { //nolint:testifylint
		return nil
	}
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Extract the metrics
	parser := expfmt.TextParser{}

	metricFamilies, err := parser.TextToMetricFamilies(resp.Body)
	assert.NoError(t, err)
	assert.NoError(t, resp.Body.Close())

	metrics := make(map[string]float64)
	for _, mf := range metricFamilies {
		for _, m := range mf.GetMetric() {
			metricName := mf.GetName()
			labels := ""
			labelMap := make(map[string]string)
			for _, l := range m.GetLabel() {
				labels += "|" + l.GetName() + ":" + l.GetValue()
				labelMap[l.GetName()] = l.GetValue()
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

func (m *Metrics) All() map[string]float64 {
	if m == nil {
		return make(map[string]float64)
	}
	return m.metrics
}

// MatchMetric returns all metrics that contain all the substrings in the key
// This is useful because of the way we serialize labels in the metrics name
func (m *Metrics) MatchMetric(substrings ...string) []Metric {
	result := make([]Metric, 0)

	// Iterate over all key-value pairs in the map
	for key, value := range m.metrics {
		matchesAll := true

		// Check if all substrings are present in the key
		for _, substring := range substrings {
			if !strings.Contains(key, substring) {
				matchesAll = false
				break
			}
		}

		// If all substrings match, add it to the result
		if matchesAll {
			result = append(result, Metric{
				Value: value,
				Name:  key,
			})
		}
	}

	return result
}

// MatchMetricAndSum returns true if the set of metrics has a specific sum
func (m *Metrics) MatchMetricAndSum(t assert.TestingT, sum float64, substrings ...string) bool {
	if !assert.NotNil(t, m, "Metrics is nil") {
		return false
	}

	// The set of metrics is determined by all metrics that match the substrings
	// The sum is the combined total of all the matching metrics' values
	metrics := m.MatchMetric(substrings...)
	matchSum := 0.0
	for _, metric := range metrics {
		matchSum += metric.Value
	}

	return matchSum == sum
}
