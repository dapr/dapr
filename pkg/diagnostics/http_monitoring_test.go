package diagnostics

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"

	"github.com/dapr/dapr/pkg/config"
)

func TestHTTPMiddleware(t *testing.T) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	testRequest := fakeHTTPRequest(requestBody)

	// create test httpMetrics
	testHTTP := newHTTPMetrics()
	configHTTP := NewHTTPMonitoringConfig(nil, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	require.NoError(t, testHTTP.Init(meter, "fakeID", configHTTP, config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)))

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	handler.ServeHTTP(httptest.NewRecorder(), testRequest)

	// assert
	rows, err := meter.RetrieveData("http/server/request_count")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
	assert.Equal(t, "fakeID", rows[0].Tags[0].Value)
	assert.Equal(t, "method", rows[0].Tags[1].Key.Name())
	assert.Equal(t, "POST", rows[0].Tags[1].Value)
	assert.Equal(t, "status", rows[0].Tags[2].Key.Name())
	assert.Equal(t, "200", rows[0].Tags[2].Value)

	rows, err = meter.RetrieveData("http/server/request_bytes")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
	assert.Equal(t, "fakeID", rows[0].Tags[0].Value)
	assert.InEpsilon(t, float64(len(requestBody)), (rows[0].Data).(*view.DistributionData).Min, 0)

	rows, err = meter.RetrieveData("http/server/response_bytes")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.InEpsilon(t, float64(len(responseBody)), (rows[0].Data).(*view.DistributionData).Min, 0)

	rows, err = meter.RetrieveData("http/server/latency")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.GreaterOrEqual(t, (rows[0].Data).(*view.DistributionData).Min, 100.0)
}

func TestHTTPMiddlewareWhenMetricsDisabled(t *testing.T) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	testRequest := fakeHTTPRequest(requestBody)

	// create test httpMetrics
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	configHTTP := NewHTTPMonitoringConfig(nil, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	require.NoError(t, testHTTP.Init(meter, "fakeID", configHTTP, config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)))
	v := meter.Find("http/server/request_count")
	views := []*view.View{v}
	meter.Unregister(views...)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	handler.ServeHTTP(httptest.NewRecorder(), testRequest)

	// assert
	rows, err := meter.RetrieveData("http/server/request_count")
	require.Error(t, err)
	assert.Nil(t, rows)
}

func TestGetMetricsMethod(t *testing.T) {
	testHTTP := newHTTPMetrics()
	configHTTP := NewHTTPMonitoringConfig(nil, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)
	assert.Equal(t, "GET", testHTTP.getMetricsMethod("GET"))
	assert.Equal(t, "POST", testHTTP.getMetricsMethod("POST"))
	assert.Equal(t, "PUT", testHTTP.getMetricsMethod("PUT"))
	assert.Equal(t, "DELETE", testHTTP.getMetricsMethod("DELETE"))
	assert.Equal(t, "PATCH", testHTTP.getMetricsMethod("PATCH"))
	assert.Equal(t, "HEAD", testHTTP.getMetricsMethod("HEAD"))
	assert.Equal(t, "OPTIONS", testHTTP.getMetricsMethod("OPTIONS"))
	assert.Equal(t, "CONNECT", testHTTP.getMetricsMethod("CONNECT"))
	assert.Equal(t, "TRACE", testHTTP.getMetricsMethod("TRACE"))
	assert.Equal(t, "UNKNOWN", testHTTP.getMetricsMethod("INVALID"))
}

func TestGetMetricsMethodExcludeVerbs(t *testing.T) {
	testHTTP := newHTTPMetrics()
	configHTTP := NewHTTPMonitoringConfig(nil, false, true)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)
	assert.Equal(t, "", testHTTP.getMetricsMethod("GET"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("POST"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("PUT"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("DELETE"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("PATCH"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("HEAD"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("OPTIONS"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("CONNECT"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("TRACE"))
	assert.Equal(t, "", testHTTP.getMetricsMethod("INVALID"))
}

func fakeHTTPRequest(body string) *http.Request {
	req, err := http.NewRequest(http.MethodPost, "http://dapr.io/invoke/method/testmethod", strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Correlation-ID", "e6f4bb20-96c0-426a-9e3d-991ba16a3ebb")
	req.Header.Set("XXX-Remote-Addr", "192.168.0.100")
	req.Header.Set("Transfer-Encoding", "encoding")
	// This is normally set automatically when the request is sent to a server, but in this case we are not using a real server
	req.Header.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))

	return req
}

func TestHTTPMiddleware_PathMatching_Scenarios(t *testing.T) {
	tests := []struct {
		name                 string
		pathMatching         []string
		increasedCardinality bool // maps to legacy
		requestPath          string
		expectedPathTag      string
	}{
		// Low Cardinality Scenarios
		{
			name:                 "Low Cardinality - With Path Matching - Matched",
			pathMatching:         []string{"/orders/{orderID}"},
			increasedCardinality: false,
			requestPath:          "/orders/123",
			expectedPathTag:      "/orders/{orderID}",
		},
		{
			name:                 "Low Cardinality - With Path Matching - Unmatched",
			pathMatching:         []string{"/orders/{orderID}"},
			increasedCardinality: false,
			requestPath:          "/unknown/path",
			expectedPathTag:      "",
		},
		{
			name:                 "Low Cardinality - No Path Matching",
			pathMatching:         nil,
			increasedCardinality: false,
			requestPath:          "/orders/123",
			expectedPathTag:      "",
		},

		// High Cardinality Scenarios
		{
			name:                 "High Cardinality - With Path Matching - Matched",
			pathMatching:         []string{"/orders/{orderID}"},
			increasedCardinality: true,
			requestPath:          "/orders/123",
			expectedPathTag:      "/orders/{orderID}",
		},
		{
			name:                 "High Cardinality - With Path Matching - Unmatched (Preserves Raw)",
			pathMatching:         []string{"/orders/{orderID}"},
			increasedCardinality: true,
			requestPath:          "/unique/path/123",
			expectedPathTag:      "/unique/path/123",
		},
		{
			name:                 "High Cardinality - No Path Matching",
			pathMatching:         nil,
			increasedCardinality: true,
			requestPath:          "/orders/1",
			expectedPathTag:      "/orders/1",
		},

		// Regression Scenarios (Actor Path)
		{
			name:                 "Regression - Actor Path - Strict Mode (Low Card) - Matched",
			pathMatching:         []string{"/v1.0/actors/MyActor/{id}/method/MyMethod"},
			increasedCardinality: false,
			requestPath:          "/v1.0/actors/MyActor/123/method/MyMethod",
			expectedPathTag:      "/v1.0/actors/MyActor/{id}/method/MyMethod",
		},
		{
			name:                 "Regression - Actor Path - Legacy Mode (High Card) - Matched",
			pathMatching:         []string{"/v1.0/actors/MyActor/{id}/method/MyMethod"},
			increasedCardinality: true,
			requestPath:          "/v1.0/actors/MyActor/123/method/MyMethod",
			expectedPathTag:      "/v1.0/actors/MyActor/{id}/method/MyMethod",
		},
		{
			name:                 "Regression - Actor Path - Legacy Mode (High Card) - Unmatched (Raw)",
			pathMatching:         []string{"/other/path"},
			increasedCardinality: true,
			requestPath:          "/v1.0/actors/MyActor/123/method/MyMethod",
			expectedPathTag:      "/v1.0/actors/MyActor/123/method/MyMethod",
		},

		{
			name:                 "Legacy: Double Slash should be Normalized (Merged) when Path Matching ON",
			pathMatching:         []string{"/v1.0/actors/myactortype/{id}/method"},
			increasedCardinality: true,
			requestPath:          "//v1.0/actors/myactortype/myid/method/foo",
			expectedPathTag:      "/v1.0/actors/myactortype/myid/method/foo",
		},
		{
			name:                 "Legacy: Normal Path should be preserved (Raw/Cleaned) when Path Matching ON",
			pathMatching:         []string{"/v1.0/actors/myactortype/{id}/method"},
			increasedCardinality: true,
			requestPath:          "/v1.0/actors/myactortype/myid/method/foo",
			expectedPathTag:      "/v1.0/actors/myactortype/myid/method/foo",
		},
		{
			name:                 "Strict: Double Slash (Unmatched) should be dropped",
			pathMatching:         []string{"/v1.0/actors/myactortype/{id}/method"},
			increasedCardinality: false,
			requestPath:          "//v1.0/actors/myactortype/myid/method/foo",
			expectedPathTag:      "",
		},
		{
			name:                 "Strict: Unmatched path should be dropped",
			pathMatching:         []string{"/v1.0/actors/myactortype/{id}/method"},
			increasedCardinality: false,
			requestPath:          "//v1.0/actors/unknown/myid/method/foo",
			expectedPathTag:      "",
		},
		// Legacy Normalization when Path Matching is OFF
		{
			name:                 "Legacy: Path Matching OFF -> Should Normalize",
			pathMatching:         nil,
			increasedCardinality: true,
			requestPath:          "/v1.0/actors/myactortype/myid/method/foo",
			expectedPathTag:      "/v1.0/actors/myactortype/{id}/method",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testHTTP := newHTTPMetrics()
			configHTTP := NewHTTPMonitoringConfig(tc.pathMatching, tc.increasedCardinality, false)
			meter := view.NewMeter()
			meter.Start()
			t.Cleanup(func() { meter.Stop() })

			require.NoError(t, testHTTP.Init(meter, "fakeID", configHTTP, config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)))

			handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			// Handle potential double slash in request path construction
			targetURL := "http://localhost:3500" + tc.requestPath
			req, _ := http.NewRequest(http.MethodPost, targetURL, nil)

			// Force raw path with double slash if needed
			if strings.HasPrefix(tc.requestPath, "//") {
				req.URL.Path = tc.requestPath
			}

			handler.ServeHTTP(httptest.NewRecorder(), req)

			rows, err := meter.RetrieveData("http/server/request_count")
			require.NoError(t, err)

			found := false
			for _, row := range rows {
				if getPathTag(row) == tc.expectedPathTag {
					found = true
					break
				}
			}

			if len(rows) > 0 {
				assert.True(t, found, "Expected path tag '%s' not found in rows for case '%s'. Found: %s", tc.expectedPathTag, tc.name, getPathTag(rows[0]))
			} else {
				require.NotEmpty(t, rows, "Expected metrics to be recorded")
			}
		})
	}
}

func getPathTag(row *view.Row) string {
	for _, tag := range row.Tags {
		if tag.Key.Name() == "path" {
			return tag.Value
		}
	}
	return ""
}
