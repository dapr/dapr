package diagnostics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/dapr/components-contrib/middleware/http/nethttpadaptor"
	"github.com/dapr/dapr/pkg/diagnostics/testdata"
	pb "github.com/dapr/dapr/pkg/diagnostics/utils"
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
	bodySplit := strings.Split(w.Body.String(), "\n")
	testDataSplit := strings.Split(testdata.BaselineMetrics, "\n")
	r := regexp.MustCompile(metricsRegex)
	for i := 0; i < len(testDataSplit); i++ {
		line := testDataSplit[i]
		if strings.HasPrefix(line, "#") || len(line) == 0 {
			continue // ignore comments
		}
		match := r.FindString(line)
		metric := match[0 : len(match)-1]
		assert.Contains(t, bodySplit[i], metric)
	}
}

func TestMetricsGRPCMiddlewareStream(t *testing.T) {
	opt := grpc_go.StreamInterceptor(MetricsGRPCMiddlewareStream())
	s := grpc_go.NewServer(opt)
	pb.RegisterGreeterServer(s, &server{})
	grpc_prometheus.Register(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	promhttp.Handler().ServeHTTP(w, req)
	assert.Contains(t, w.Body.String(), testdata.GRPCStreamMetrics)
}

func TestMetricsGRPCMiddlewareUnary(t *testing.T) {
	opt := grpc_go.UnaryInterceptor(MetricsGRPCMiddlewareUnary())
	s := grpc_go.NewServer(opt)
	pb.RegisterGreeterServer(s, &server{})
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

	r := regexp.MustCompile(metricsRegex)
	// Build a map of expected metrics
	for i := 0; i < len(testDataSplit); i++ {
		line := testDataSplit[i]
		if strings.HasPrefix(line, "#") {
			continue // ignore comments
		}

		match := r.FindString(line)
		metric := match[0 : len(match)-1]
		testDataMap[metric] = false
	}

	// Find the metrics that are in the actual data
	for j := 0; j < len(bodySplit); j++ {
		line := bodySplit[j]
		if strings.HasPrefix(line, "#") || len(line) == 0 {
			continue // ignore comments
		}
		match := r.FindString(line)
		metric := match[0 : len(match)-1]
		if _, ok := testDataMap[metric]; ok {
			testDataMap[metric] = true
		}
	}

	// Check all were found
	for k, v := range testDataMap {
		assert.True(t, v, fmt.Sprintf("expected metric %s was missing!", k))
	}
}
