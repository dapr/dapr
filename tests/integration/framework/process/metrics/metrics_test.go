package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const exampleMetrics = `
# TYPE dapr_placement_actor_heartbeat_timestamp gauge
dapr_placement_actor_heartbeat_timestamp{actor_type="a1",app_id="actors1",host_name="hst1",host_namespace="ns1",pod_name=""} 1.729720899e+09
# TYPE dapr_placement_actor_runtimes_total gauge
dapr_placement_actor_runtimes_total{host_namespace="ns1"} 2
# TYPE dapr_placement_runtimes_total gauge
dapr_placement_runtimes_total{host_namespace="ns1"} 3
`

func TestNew(t *testing.T) {
	// Create a mock HTTP server that serves example metrics data
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(w, strings.NewReader(exampleMetrics))
		assert.NoError(t, err)
	}))
	defer ts.Close()

	metrics := New(t, context.Background(), ts.URL)
	metricsMap := metrics.All()

	require.NotNil(t, metricsMap)
	require.Len(t, metricsMap, 3)

	require.Contains(t, metricsMap, `dapr_placement_actor_heartbeat_timestamp|actor_type:a1|app_id:actors1|host_name:hst1|host_namespace:ns1|pod_name:`)
	require.Contains(t, metricsMap, `dapr_placement_actor_runtimes_total|host_namespace:ns1`)
	require.Contains(t, metricsMap, `dapr_placement_runtimes_total|host_namespace:ns1`)
	require.Equal(t, 1729720899, int(metricsMap["dapr_placement_actor_heartbeat_timestamp|actor_type:a1|app_id:actors1|host_name:hst1|host_namespace:ns1|pod_name:"]))
	require.Equal(t, 2, int(metricsMap["dapr_placement_actor_runtimes_total|host_namespace:ns1"]))
	require.Equal(t, 3, int(metricsMap["dapr_placement_runtimes_total|host_namespace:ns1"]))
}

func TestMatchMetric(t *testing.T) {
	// Simulate parsed metrics
	metrics := &Metrics{
		metrics: map[string]float64{
			"dapr_placement_actor_heartbeat_timestamp|actor_type:a1|app_id:actors1|host_name:hst1|host_namespace:ns1|pod_name:": 123456789,
			"dapr_placement_actor_runtimes_total|host_namespace:ns1":                                                            3,
		},
	}

	results := metrics.MatchMetric("dapr_placement_actor_runtimes_total")
	require.Len(t, results, 1)
	require.Equal(t, 3, int(results[0].Value))

	results = metrics.MatchMetric("actor_type:a1", "app_id:actors1")
	require.Len(t, results, 1)
	require.Equal(t, 123456789, int(results[0].Value))

	results = metrics.MatchMetric("dapr_placement_actor_runtimes_total:host_namespace:ns2")
	require.Empty(t, results)

	results = metrics.MatchMetric("actor_type:non-existent")
	require.Empty(t, results)
}
