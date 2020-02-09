package diagnostics

import (
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/middleware/http/nethttpadaptor"
	"github.com/dapr/dapr/pkg/config"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	http_metrics "github.com/improbable-eng/go-httpwares/metrics"
	http_prometheus "github.com/improbable-eng/go-httpwares/metrics/prometheus"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	grpc_go "google.golang.org/grpc"
)

const (
	httpServerMetricsGroup       = "http_server"
	grpcStreamServerMetricsGroup = "grpc_stream_server"
	grpcUnaryServerMetricsGroup  = "grpc_unary_server"
)

// MetricsGRPCMiddlewareStream gets a metrics enabled GRPC stream middlware
func MetricsGRPCMiddlewareStream(spec config.MetricsSpec) grpc_go.StreamServerInterceptor {
	if spec.UseDefaults {
		spec.MetricsGroups = getDefaultMetrics()
	}
	var inteceptor grpc_go.StreamServerInterceptor
	for _, metricsGroup := range spec.MetricsGroups {
		if strings.EqualFold(metricsGroup, grpcStreamServerMetricsGroup) {
			inteceptor = grpc_prometheus.StreamServerInterceptor
		}
	}
	return inteceptor
}

// MetricsGRPCMiddlewareUnary gets a metrics enabled GRPC unary middlware
func MetricsGRPCMiddlewareUnary(spec config.MetricsSpec) grpc_go.UnaryServerInterceptor {
	if spec.UseDefaults {
		spec.MetricsGroups = getDefaultMetrics()
	}
	var inteceptor grpc_go.UnaryServerInterceptor
	for _, metricsGroup := range spec.MetricsGroups {
		if strings.EqualFold(metricsGroup, grpcUnaryServerMetricsGroup) {
			inteceptor = grpc_prometheus.UnaryServerInterceptor
		}
	}
	return inteceptor
}

// MetricsHTTPMiddleware gets a metrics enabled HTTP middleware
func MetricsHTTPMiddleware(spec config.MetricsSpec, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if spec.UseDefaults {
		spec.MetricsGroups = getDefaultMetrics()
	}
	wrapped := next
	for _, metricsGroup := range spec.MetricsGroups {
		if strings.EqualFold(metricsGroup, httpServerMetricsGroup) {
			// TODO: support custom labels
			mw := http_metrics.Middleware(http_prometheus.ServerMetrics(
				http_prometheus.WithName(fmt.Sprintf("%s-daprd", spec.Namespace)),
				http_prometheus.WithHostLabel(),
				http_prometheus.WithLatency(),
				http_prometheus.WithSizes(),
				http_prometheus.WithPathLabel()))
			wrapped = fasthttpadaptor.NewFastHTTPHandler(mw(nethttpadaptor.NewNetHTTPHandlerFunc(next)))
		}
	}
	return wrapped
}

func getDefaultMetrics() []string {
	return []string{
		httpServerMetricsGroup,
		grpcStreamServerMetricsGroup,
	}
}
