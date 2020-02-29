package diagnostics

import (
	"github.com/dapr/components-contrib/middleware/http/nethttpadaptor"
	"github.com/dapr/dapr/pkg/logger"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	http_metrics "github.com/improbable-eng/go-httpwares/metrics"
	http_prometheus "github.com/improbable-eng/go-httpwares/metrics/prometheus"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	grpc_go "google.golang.org/grpc"
)

var log = logger.NewLogger("diagnostics.metrics")

// MetricsGRPCMiddlewareStream gets a metrics enabled GRPC stream middlware
func MetricsGRPCMiddlewareStream() grpc_go.StreamServerInterceptor {
	return grpc_prometheus.StreamServerInterceptor
}

// MetricsGRPCMiddlewareUnary gets a metrics enabled GRPC unary middlware
func MetricsGRPCMiddlewareUnary() grpc_go.UnaryServerInterceptor {
	return grpc_prometheus.UnaryServerInterceptor
}

// MetricsHTTPMiddleware gets a metrics enabled HTTP middleware
func MetricsHTTPMiddleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	// TODO: support custom labels
	mw := http_metrics.Middleware(http_prometheus.ServerMetrics(
		http_prometheus.WithName("daprd"),
		http_prometheus.WithHostLabel(),
		http_prometheus.WithLatency(),
		http_prometheus.WithSizes(),
		http_prometheus.WithPathLabel()))
	return fasthttpadaptor.NewFastHTTPHandler(mw(nethttpadaptor.NewNetHTTPHandlerFunc(log, next)))
}
