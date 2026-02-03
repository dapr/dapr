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

func TestHTTPMetricsPathMatchingNotEnabled(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", HTTPMonitoringConfig{}, nil)
	matchedPath, ok := testHTTP.pathMatcher.match("/orders")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)
}

func TestHTTPMetricsPathMatchingLegacyIncreasedCardinality(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{
		"/v1/orders/{orderID}/items/12345",
		"/v1/orders/{orderID}/items/{itemID}",
		"/v1/items/{itemID}",
		"/v1/orders/{orderID}/items/{itemID}",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, true, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	// act & assert

	// empty path
	matchedPath, ok := testHTTP.pathMatcher.match("")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)

	// match "/v1/orders/{orderID}/items/12345"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/12345", matchedPath)

	// match "/v1/orders/{orderID}/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/1111")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/{itemID}", matchedPath)

	// match "/v1/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/items/{itemID}", matchedPath)

	// no match so we keep the path as is
	matchedPath, ok = testHTTP.pathMatcher.match("/v2/basket/12345")
	require.True(t, ok)
	require.Equal(t, "/v2/basket/12345", matchedPath)

	// match "/v1/orders/{orderID}/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/1111")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/{itemID}", matchedPath)
}

func TestHTTPMetricsPathMatchingLowCardinality(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{
		"/v1/orders/{orderID}/items/12345",
		"/v1/orders/{orderID}/items/{itemID}",
		"/v1/orders/{orderID}",
		"/v1/items/{itemID}",
		"/dapr/config",
		"/v1/",
		"/",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	// act & assert

	// empty path
	matchedPath, ok := testHTTP.pathMatcher.match("")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)

	// match "/v1/orders/{orderID}/items/12345"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/12345", matchedPath)

	// match "/v1/orders/{orderID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/orders/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}", matchedPath)

	// match "/v1/items/{itemID}"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/items/{itemID}", matchedPath)

	// match "/v1/"
	matchedPath, ok = testHTTP.pathMatcher.match("/v1/basket")
	require.True(t, ok)
	assert.Equal(t, "/v1/", matchedPath)

	// match "/"
	matchedPath, ok = testHTTP.pathMatcher.match("/v2/orders/1111")
	require.True(t, ok)
	assert.Equal(t, "/", matchedPath)

	// no match so we fallback to "/"
	matchedPath, ok = testHTTP.pathMatcher.match("/basket/12345")
	require.True(t, ok)
	require.Equal(t, "/", matchedPath)

	matchedPath, ok = testHTTP.pathMatcher.match("/dapr/config")
	require.True(t, ok)
	require.Equal(t, "/dapr/config", matchedPath)
}

func TestHTTPMetricsPathMatchingLowCardinalityRootPathRegister(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false

	// 1 - Root path not registered fallback to ""
	paths1 := []string{"/v1/orders/{orderID}"}
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", HTTPMonitoringConfig{paths1, false, false}, nil)
	matchedPath, ok := testHTTP.pathMatcher.match("/thispathdoesnotexist")
	require.True(t, ok)
	require.Equal(t, "", matchedPath)

	// 2 - Root path registered fallback to "/"
	paths2 := []string{"/v1/orders/{orderID}", "/"}
	meter2 := view.NewMeter()
	meter2.Start()
	defer meter2.Stop()
	testHTTP.Init(meter2, "fakeID", HTTPMonitoringConfig{paths2, false, false}, nil)
	matchedPath, ok = testHTTP.pathMatcher.match("/thispathdoesnotexist")
	require.True(t, ok)
	require.Equal(t, "/", matchedPath)
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

func TestHTTPMetricsPathMatchingWithRedirect(t *testing.T) {
	const testPath = "/redirect-test"

	pm := newPathMatching([]string{"/other-path"}, false)
	pm.mux.HandleFunc(testPath, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	})

	matchedPath, ok := pm.match(testPath)
	require.True(t, ok, "Expected path matching to succeed")
	require.Equal(t, testPath, matchedPath, "Expected matched path to be %q", testPath)
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

func TestHTTPMiddleware_Normalization(t *testing.T) {
	paths := []string{"/v1.0/actors/myactortype/{id}/method"}

	testCases := []struct {
		name         string
		requestPath  string
		legacy       bool
		expectedPath string
		expectMatch  bool
	}{
		{
			name:         "Legacy: Double Slash should be normalized and ID masked",
			requestPath:  "//v1.0/actors/myactortype/myid/method/foo",
			legacy:       true,
			expectedPath: "/v1.0/actors/myactortype/{id}/method",
			expectMatch:  true,
		},
		{
			name:         "Legacy: Normal Path",
			requestPath:  "/v1.0/actors/myactortype/myid/method/foo",
			legacy:       true,
			expectedPath: "/v1.0/actors/myactortype/{id}/method",
			expectMatch:  true,
		},
		{
			name:         "Strict: Double Slash should be normalized and matched",
			requestPath:  "//v1.0/actors/myactortype/myid/method/foo",
			legacy:       false,
			expectedPath: "/v1.0/actors/myactortype/{id}/method",
			expectMatch:  true,
		},
		{
			name:         "Strict: Unmatched path (Double Slash) should be dropped",
			requestPath:  "//v1.0/actors/unknown/myid/method/foo",
			legacy:       false,
			expectedPath: "",
			expectMatch:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testHTTP := newHTTPMetrics()
			configHTTP := NewHTTPMonitoringConfig(paths, tc.legacy, false)
			meter := view.NewMeter()
			meter.Start()
			t.Cleanup(func() { meter.Stop() })

			require.NoError(t, testHTTP.Init(meter, "fakeID", configHTTP, config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)))
			handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req, _ := http.NewRequest(http.MethodPut, "http://localhost:3500"+tc.requestPath, nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			rows, err := meter.RetrieveData("http/server/request_count")
			require.NoError(t, err)

			if tc.expectMatch {
				require.Len(t, rows, 1, "Expected 1 metric row")
				pathTag := getPathTag(rows[0])
				assert.Equal(t, tc.expectedPath, pathTag, "Path tag mismatch")
			} else if len(rows) > 0 {
				pathTag := getPathTag(rows[0])
				assert.Equal(t, "", pathTag, "Expected empty path for unmatched request")
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

func TestHTTPMetricsPathMatchingActorEndpoints(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{
		"/v1.0/actors/{actorType}/{actorId}/method/{method}",
		"/v1.0/actors/{actorType}/{actorId}/reminders/{reminder}",
		"/v1.0/actors/{actorType}/{actorId}/timers/{timer}",
		"/v1.0/actors/{actorType}/{actorId}/state/{key}",
		"/v1.0/actors/{actorType}/{actorId}/state",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	tests := []struct {
		name          string
		path          string
		expectedMatch string
	}{
		{
			name:          "actor method invocation",
			path:          "/v1.0/actors/WorkerActor/ActorID/method/StoreModelAndConstructRequest",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/method/{method}",
		},
		{
			name:          "actor reminder",
			path:          "/v1.0/actors/WorkerActor/ActorID/reminders/MyReminder",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/reminders/{reminder}",
		},
		{
			name:          "actor timer",
			path:          "/v1.0/actors/WorkerActor/ActorID/timers/MyTimer",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/timers/{timer}",
		},
		{
			name:          "actor state get specific key",
			path:          "/v1.0/actors/RxPassResultActor/ActorID/state/stateKey",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/state/{key}",
		},
		{
			name:          "actor state transaction",
			path:          "/v1.0/actors/OrderActor/order-123/state",
			expectedMatch: "/v1.0/actors/{actorType}/{actorId}/state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchedPath, ok := testHTTP.pathMatcher.match(tt.path)
			require.True(t, ok)
			require.Equal(t, tt.expectedMatch, matchedPath)
		})
	}
}

func TestHTTPMetricsPathMatchingUnmatchedPathWithSubtreeStrictMode(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	paths := []string{"/"}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	matchedPath, ok := testHTTP.pathMatcher.match("//v1.0/actors/WeatherActor/xyz/method/GetWeatherAsync")
	require.True(t, ok)
	require.Equal(t, "/", matchedPath, "double-slash actor path should match root '/' in strict mode")
}

func TestHTTPMetricsPathMatchingServiceInvocation(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false

	paths := []string{
		"/orders/{id}",
		"/api/v1/widget",
	}
	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() {
		meter.Stop()
	})
	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	matchedPath, ok := testHTTP.pathMatcher.match("/orders/123")
	require.True(t, ok)
	assert.Equal(t, "/orders/{id}", matchedPath)

	invokePath := "/v1.0/invoke/order-app/method/orders/123"
	matchedPath, ok = testHTTP.pathMatcher.match(invokePath)
	require.True(t, ok)
	assert.Equal(t, "/v1.0/invoke/{app_id}/method/orders/{id}", matchedPath)
}

func TestHTTPMetricsPathMatchingNormalizationDedup(t *testing.T) {
	paths := []string{
		"/orders",
		"//orders",
		"orders",
		"///orders",
	}

	configHTTP := NewHTTPMonitoringConfig(paths, false, false)
	testHTTP := newHTTPMetrics()

	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(func() { meter.Stop() })

	testHTTP.Init(meter, "fakeID", configHTTP, nil)

	matchedPath, ok := testHTTP.pathMatcher.match("//orders")
	require.True(t, ok)
	assert.Equal(t, "/orders", matchedPath)
}
