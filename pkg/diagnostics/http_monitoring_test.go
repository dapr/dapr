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
	testHTTP.Init("fakeID", nil, false)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	handler.ServeHTTP(httptest.NewRecorder(), testRequest)

	// assert
	rows, err := view.RetrieveData("http/server/request_count")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
	assert.Equal(t, "fakeID", rows[0].Tags[0].Value)
	assert.Equal(t, "status", rows[0].Tags[1].Key.Name())
	assert.Equal(t, "200", rows[0].Tags[1].Value)

	rows, err = view.RetrieveData("http/server/request_bytes")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.Equal(t, "app_id", rows[0].Tags[0].Key.Name())
	assert.Equal(t, "fakeID", rows[0].Tags[0].Value)
	assert.InEpsilon(t, float64(len(requestBody)), (rows[0].Data).(*view.DistributionData).Min, 0)

	rows, err = view.RetrieveData("http/server/response_bytes")
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.InEpsilon(t, float64(len(responseBody)), (rows[0].Data).(*view.DistributionData).Min, 0)

	rows, err = view.RetrieveData("http/server/latency")
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
	testHTTP.Init("fakeID", nil, false)
	v := view.Find("http/server/request_count")
	views := []*view.View{v}
	view.Unregister(views...)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act
	handler.ServeHTTP(httptest.NewRecorder(), testRequest)

	// assert
	rows, err := view.RetrieveData("http/server/request_count")
	require.Error(t, err)
	assert.Nil(t, rows)
}

func TestHTTPMetricsPathMatchingNotEnabled(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	pathMatching := &config.PathMatching{}
	testHTTP.Init("fakeID", pathMatching, true)
	matchedPath, ok := testHTTP.ingress.matchPath("/orders")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)
}

func TestHTTPMetricsPathMatchingLegacyIncreasedCardinality(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	config := &config.PathMatching{
		IngressPaths: []string{
			"/v1/orders/{orderID}/items/12345",
			"/v1/orders/{orderID}/items/{itemID}",
			"/v1/items/{itemID}",
		},
		EgressPaths: []string{
			"/v1/orders/{orderID}/items/{itemID}",
		},
	}
	testHTTP.Init("fakeID", config, true)

	// act & assert

	// empty path
	matchedPath, ok := testHTTP.ingress.matchPath("")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)

	// match "/v1/orders/{orderID}/items/12345"
	matchedPath, ok = testHTTP.ingress.matchPath("/v1/orders/12345/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/12345", matchedPath)

	// match "/v1/orders/{orderID}/items/{itemID}"
	matchedPath, ok = testHTTP.ingress.matchPath("/v1/orders/12345/items/1111")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/{itemID}", matchedPath)

	// match "/v1/items/{itemID}"
	matchedPath, ok = testHTTP.ingress.matchPath("/v1/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/items/{itemID}", matchedPath)

	// no match so we keep the path as is
	matchedPath, ok = testHTTP.ingress.matchPath("/v2/basket/12345")
	require.True(t, ok)
	require.Equal(t, "/v2/basket/12345", matchedPath)

	// match "/v1/orders/{orderID}/items/{itemID}"
	matchedPath, ok = testHTTP.egress.matchPath("/v1/orders/12345/items/1111")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/{itemID}", matchedPath)

	// no match so we keep the path as is
	matchedPath, ok = testHTTP.egress.matchPath("/v1/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/items/12345", matchedPath)
}

func TestHTTPMetricsPathMatchingLowCardinality(t *testing.T) {
	testHTTP := newHTTPMetrics()
	testHTTP.enabled = false
	config := &config.PathMatching{
		IngressPaths: []string{
			"/v1/orders/{orderID}/items/12345",
			"/v1/orders/{orderID}/items/{itemID}",
			"/v1/orders/{orderID}",
			"/v1/items/{itemID}",
			"/dapr/config",
			"/v1/",
			"/",
		},
		EgressPaths: []string{
			"/v1/orders/{orderID}/items/{itemID}",
			"/dapr/config",
		},
	}
	testHTTP.Init("fakeID", config, false)

	// act & assert

	// empty path
	matchedPath, ok := testHTTP.ingress.matchPath("")
	require.False(t, ok)
	require.Equal(t, "", matchedPath)

	// match "/v1/orders/{orderID}/items/12345"
	matchedPath, ok = testHTTP.ingress.matchPath("/v1/orders/12345/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}/items/12345", matchedPath)

	// match "/v1/orders/{orderID}"
	matchedPath, ok = testHTTP.ingress.matchPath("/v1/orders/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/orders/{orderID}", matchedPath)

	// match "/v1/items/{itemID}"
	matchedPath, ok = testHTTP.ingress.matchPath("/v1/items/12345")
	require.True(t, ok)
	require.Equal(t, "/v1/items/{itemID}", matchedPath)

	// match "/v1/"
	matchedPath, ok = testHTTP.ingress.matchPath("/v1/basket")
	require.True(t, ok)
	assert.Equal(t, "/v1/", matchedPath)

	// match "/"
	matchedPath, ok = testHTTP.ingress.matchPath("/v2/orders/1111")
	require.True(t, ok)
	assert.Equal(t, "/", matchedPath)

	// no match so we fallback to "/"
	matchedPath, ok = testHTTP.egress.matchPath("/basket/12345")
	require.True(t, ok)
	require.Equal(t, "/", matchedPath)

	matchedPath, ok = testHTTP.egress.matchPath("/dapr/config")
	require.True(t, ok)
	require.Equal(t, "/dapr/config", matchedPath)
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
