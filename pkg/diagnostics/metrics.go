package diagnostics

import (
	"strings"

	"contrib.go.opencensus.io/exporter/prometheus"
	"github.com/dapr/components-contrib/middleware/http/nethttpadaptor"
	"github.com/dapr/dapr/pkg/config"
	routing "github.com/qiangxue/fasthttp-routing"
	log "github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/stats/view"
)

const (
	httpServerMetricsGroup = "http_server"
)

// MetricsHTTPMiddleware gets a metrics enabled HTTP middleware
func MetricsHTTPMiddleware(spec config.MetricsSpec, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if spec.UseDefaultMetrics {
		spec.EnabledMetricsGroups = getDefaultMetrics()
	}
	och := &ochttp.Handler{Handler: nethttpadaptor.NewNetHTTPHandlerFunc(next)}
	for _, metricsGroup := range spec.EnabledMetricsGroups {
		if strings.EqualFold(strings.ToLower(metricsGroup), httpServerMetricsGroup) {
			if err := view.Register(ochttp.DefaultServerViews...); err != nil {
				log.Fatalf("failed to register server views for HTTP metrics: %v", err)
			}
		}
	}
	return fasthttpadaptor.NewFastHTTPHandler(och)
}

// MetricsHTTPRouteFunc gets a metrics route builder function
func MetricsHTTPRouteFunc(daprID string, spec config.MetricsSpec) func() (string, string, []routing.Handler) {
	path := "/metrics"
	if spec.Route != "" {
		path = spec.Route
		if path[0] != '/' {
			path = "/" + path
		}
	}
	methods := "GET"
	exporter := metricsHTTPExporter(daprID, spec)
	handlers := []routing.Handler{
		func(c *routing.Context) error {
			exporter(c.RequestCtx)
			return nil
		},
	}

	return func() (string, string, []routing.Handler) {
		return methods, path, handlers
	}
}

func getDefaultMetrics() []string {
	return []string{
		httpServerMetricsGroup,
	}
}

func metricsHTTPExporter(daprID string, spec config.MetricsSpec) fasthttp.RequestHandler {
	namespace := daprID
	if spec.Namespace != "" {
		namespace = spec.Namespace
	}
	pe, err := prometheus.NewExporter(prometheus.Options{
		Namespace:   namespace,
		ConstLabels: spec.Labels,
	})
	if err != nil {
		log.Fatalf("failed to create Prometheus exporter: %v", err)
	}
	view.RegisterExporter(pe)
	return fasthttpadaptor.NewFastHTTPHandlerFunc(pe.ServeHTTP)
}
