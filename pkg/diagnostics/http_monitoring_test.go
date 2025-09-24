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
