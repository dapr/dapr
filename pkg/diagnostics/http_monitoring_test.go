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
)

func TestHTTPMiddleware(t *testing.T) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	testRequest := fakeHTTPRequest(requestBody)

	// create test httpMetrics
	testHTTP := newHTTPMetrics()
	testHTTP.Init("fakeID")

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

	testHTTP.Init("fakeID")
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

func TestConvertPathToMethodName(t *testing.T) {
	convertTests := []struct {
		in  string
		out string
	}{
		{"/v1/state/statestore/key", "/v1/state/statestore"},
		{"/v1/state/statestore", "/v1/state/statestore"},
		{"/v1/secrets/keyvault/name", "/v1/secrets/keyvault"},
		{"/v1/publish/topic", "/v1/publish/topic"},
		{"/v1/bindings/kafka", "/v1/bindings/kafka"},
		{"/healthz", "/healthz"},
		{"/v1/actors/DemoActor/1/state/key", "/v1/actors/DemoActor/{id}/state"},
		{"/v1/actors/DemoActor/1/reminder/name", "/v1/actors/DemoActor/{id}/reminder"},
		{"/v1/actors/DemoActor/1/timer/name", "/v1/actors/DemoActor/{id}/timer"},
		{"/v1/actors/DemoActor/1/timer/name?query=string", "/v1/actors/DemoActor/{id}/timer"},
		{"v1/actors/DemoActor/1/timer/name", "/v1/actors/DemoActor/{id}/timer"},
		{"actors/DemoActor/1/method/method1", "actors/DemoActor/{id}/method/method1"},
		{"actors/DemoActor/1/method/timer/timer1", "actors/DemoActor/{id}/method/timer/timer1"},
		{"actors/DemoActor/1/method/remind/reminder1", "actors/DemoActor/{id}/method/remind/reminder1"},
		{"/v1.0-alpha1/workflows/workflowComponentName/mywf/start?instanceID=1234", "/v1.0-alpha1/workflows/workflowComponentName/mywf/start"},
		{"/v1.0-alpha1/workflows/workflowComponentName/mywf/start", "/v1.0-alpha1/workflows/workflowComponentName/mywf/start"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/start/value1/value2", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/start"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/terminate", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/terminate"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/terminate/value1/value2", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/terminate"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/raiseEvent/foobaz", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/raiseEvent/{eventName}"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/pause", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/pause"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/resume", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/resume"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234/purge", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}/purge"},
		{"/v1.0-alpha1/workflows/workflowComponentName/1234", "/v1.0-alpha1/workflows/workflowComponentName/{instanceId}"},
		{"/v1.0-alpha1/workflows/workflowComponentName", "/v1.0-alpha1/workflows/workflowComponentName"},
		{"/v1.0-alpha1/workflows", "/v1.0-alpha1/workflows"},
		{"", ""},
	}

	testHTTP := newHTTPMetrics()
	for _, tt := range convertTests {
		t.Run(tt.in, func(t *testing.T) {
			lowCardinalityName := testHTTP.convertPathToMetricLabel(tt.in)
			assert.Equal(t, tt.out, lowCardinalityName)
		})
	}
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
