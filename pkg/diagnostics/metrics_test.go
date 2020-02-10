package diagnostics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dapr/components-contrib/middleware/http/nethttpadaptor"
	"github.com/dapr/dapr/pkg/diagnostics/testdata"
	helloworld "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	grpc_go "google.golang.org/grpc"
)

type server struct {
	helloworld.UnimplementedGreeterServer
}

func TestBaselineMetrics(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	promhttp.Handler().ServeHTTP(w, req)
	bodySplit := strings.Split(w.Body.String(), "\n")
	testDataSplit := strings.Split(testdata.BaselineMetrics, "\n")
	for i := 0; i < len(testDataSplit); i++ {
		if strings.HasPrefix(testDataSplit[i], "#") {
			continue // ignore comments
		}
		metric := strings.Split(testDataSplit[i], " ")[0]
		assert.Contains(t, bodySplit[i], metric)
	}
}

func TestMetricsGRPCMiddlewareStream(t *testing.T) {
	opt := grpc_go.StreamInterceptor(MetricsGRPCMiddlewareStream())
	s := grpc_go.NewServer(opt)
	helloworld.RegisterGreeterServer(s, &server{})
	grpc_prometheus.Register(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	promhttp.Handler().ServeHTTP(w, req)
	assert.Contains(t, w.Body.String(), testdata.GRPCStreamMetrics)
}

func TestMetricsGRPCMiddlewareUnary(t *testing.T) {
	opt := grpc_go.UnaryInterceptor(MetricsGRPCMiddlewareUnary())
	s := grpc_go.NewServer(opt)
	helloworld.RegisterGreeterServer(s, &server{})
	grpc_prometheus.Register(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	promhttp.Handler().ServeHTTP(w, req)
	assert.Contains(t, w.Body.String(), testdata.GRPCUnaryMetrics)
}

func TestMetricsHTTPMiddleware(t *testing.T) {
	inner := func(ctx *fasthttp.RequestCtx) {}
	mw := MetricsHTTPMiddleware(inner)
	outter := nethttpadaptor.NewNetHTTPHandlerFunc(mw)

	// HTTP metrics are not initialized with a zero value
	// so we have to invoke the metrics middleware manually.
	mwResWriter := httptest.NewRecorder()
	mwReq, _ := http.NewRequest("GET", "/", strings.NewReader(""))
	outter.ServeHTTP(mwResWriter, mwReq)

	metricsResWriter := httptest.NewRecorder()
	metricsReq, _ := http.NewRequest("GET", "/", strings.NewReader(""))
	promhttp.Handler().ServeHTTP(metricsResWriter, metricsReq)

	bodySplit := strings.Split(metricsResWriter.Body.String(), "\n")
	testDataSplit := strings.Split(testdata.HTTPServerMetrics, "\n")
	testDataMap := make(map[string]bool)

	// Build a map of expected metrics
	for i := 0; i < len(testDataSplit); i++ {
		if strings.HasPrefix(testDataSplit[i], "#") {
			continue // ignore comments
		}
		metric := strings.Split(testDataSplit[i], " ")[0]
		testDataMap[metric] = false
	}

	// Find the metrics that are in the actual data
	for j := 0; j < len(bodySplit); j++ {
		if strings.HasPrefix(bodySplit[j], "#") {
			continue // ignore comments
		}
		metric := strings.Split(bodySplit[j], " ")[0]
		if _, ok := testDataMap[metric]; ok {
			testDataMap[metric] = true
		}
	}

	// Check all were found
	for k, v := range testDataMap {
		assert.True(t, v, fmt.Sprintf("expected metric %s was missing!", k))
	}
}
