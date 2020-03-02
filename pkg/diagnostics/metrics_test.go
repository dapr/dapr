package diagnostics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/dapr/components-contrib/middleware/http/nethttpadaptor"
	"github.com/dapr/dapr/pkg/diagnostics/testdata"
	pb "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"

	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	grpc_go "google.golang.org/grpc"
)

const (
	metricsRegex = `.+?([{\s])`
)

type server struct {
	pb.UnimplementedGreeterServer
}

func TestBaselineMetrics(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	promhttp.Handler().ServeHTTP(w, req)
	assertMetricsExist(t, w.Body.String(), testdata.BaselineMetrics)
}

func TestMetricsGRPCMiddlewareStream(t *testing.T) {
	opt := grpc_go.StreamInterceptor(MetricsGRPCMiddlewareStream())
	s := grpc_go.NewServer(opt)
	pb.RegisterGreeterServer(s, &server{})
	grpc_prometheus.Register(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	promhttp.Handler().ServeHTTP(w, req)
	assertMetricsExist(t, w.Body.String(), testdata.GRPCStreamMetrics)
}

func TestMetricsGRPCMiddlewareUnary(t *testing.T) {
	opt := grpc_go.UnaryInterceptor(MetricsGRPCMiddlewareUnary())
	s := grpc_go.NewServer(opt)
	pb.RegisterGreeterServer(s, &server{})
	grpc_prometheus.Register(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	promhttp.Handler().ServeHTTP(w, req)
	assertMetricsExist(t, w.Body.String(), testdata.GRPCUnaryMetrics)
}

func TestMetricsHTTPMiddleware(t *testing.T) {
	fakeLogger := logger.NewLogger("test")
	inner := func(ctx *fasthttp.RequestCtx) {}
	mw := MetricsHTTPMiddleware(inner)
	outter := nethttpadaptor.NewNetHTTPHandlerFunc(fakeLogger, mw)

	// HTTP metrics are not initialized with a zero value
	// so we have to invoke the metrics middleware manually.
	mwResWriter := httptest.NewRecorder()
	mwReq, _ := http.NewRequest("GET", "/", strings.NewReader(""))
	outter.ServeHTTP(mwResWriter, mwReq)

	metricsResWriter := httptest.NewRecorder()
	metricsReq, _ := http.NewRequest("GET", "/", strings.NewReader(""))
	promhttp.Handler().ServeHTTP(metricsResWriter, metricsReq)

	assertMetricsExist(t, metricsResWriter.Body.String(), testdata.HTTPServerMetrics)
}

func assertMetricsExist(t *testing.T, actual, expected string) {
	r := regexp.MustCompile(metricsRegex)

	actualLines := strings.Split(actual, "\n")
	expectedLines := strings.Split(expected, "\n")

	// Build a map of expected metrics
	expectedMap := make(map[string]bool)
	for i := 0; i < len(expectedLines); i++ {
		expected := expectedLines[i]
		if strings.HasPrefix(expected, "#") {
			continue // ignore comments and empty lines
		}
		match := r.FindString(expected)
		metric := match[0 : len(match)-1]
		expectedMap[metric] = false
	}

	// Remove metrics not on platform
	switch runtime.GOOS {
	case "darwin":
		delete(expectedMap, "process_cpu_seconds_total")
		delete(expectedMap, "process_start_time_seconds")
		delete(expectedMap, "process_open_fds")
		delete(expectedMap, "process_max_fds")
		delete(expectedMap, "process_resident_memory_bytes")
		delete(expectedMap, "process_virtual_memory_bytes")
		delete(expectedMap, "process_virtual_memory_max_bytes")
	case "windows":
		delete(expectedMap, "process_virtual_memory_max_bytes")
	}

	// Find the metrics that are in the actual data
	for j := 0; j < len(actualLines); j++ {
		actual := actualLines[j]
		if strings.HasPrefix(actual, "#") || len(actual) == 0 {
			continue // ignore comments and empty lines
		}
		match := r.FindString(actual)
		metric := match[0 : len(match)-1]
		if _, ok := expectedMap[metric]; ok {
			expectedMap[metric] = true
		}
	}

	// Check all were found
	for k, v := range expectedMap {
		assert.True(t, v, fmt.Sprintf("expected metric %s was missing!", k))
	}
}
