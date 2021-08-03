// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

// To track the metrics for fasthttp using opencensus, this implementation is inspired by
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ochttp

// Tag key definitions for http requests.
var (
	httpStatusCodeKey = tag.MustNewKey("status")
	httpPathKey       = tag.MustNewKey("path")
	httpMethodKey     = tag.MustNewKey("method")
)

// Default distributions.
var (
	defaultSizeDistribution    = view.Distribution(1024, 2048, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824, 4294967296)
	defaultLatencyDistribution = view.Distribution(1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1000, 2000, 5000, 10000, 20000, 50000, 100000)
)

type httpMetrics struct {
	serverRequestCount  *stats.Int64Measure
	serverRequestBytes  *stats.Int64Measure
	serverResponseBytes *stats.Int64Measure
	serverLatency       *stats.Float64Measure
	serverResponseCount *stats.Int64Measure

	clientSentBytes        *stats.Int64Measure
	clientReceivedBytes    *stats.Int64Measure
	clientRoundtripLatency *stats.Float64Measure
	clientCompletedCount   *stats.Int64Measure

	appID   string
	enabled bool
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{
		serverRequestCount: stats.Int64(
			"http/server/request_count",
			"Number of HTTP requests started in server.",
			stats.UnitDimensionless),
		serverRequestBytes: stats.Int64(
			"http/server/request_bytes",
			"HTTP request body size if set as ContentLength (uncompressed) in server.",
			stats.UnitBytes),
		serverResponseBytes: stats.Int64(
			"http/server/response_bytes",
			"HTTP response body size (uncompressed) in server.",
			stats.UnitBytes),
		serverLatency: stats.Float64(
			"http/server/latency",
			"HTTP request end to end latency in server.",
			stats.UnitMilliseconds),
		serverResponseCount: stats.Int64(
			"http/server/response_count",
			"The number of HTTP responses",
			stats.UnitDimensionless),
		clientSentBytes: stats.Int64(
			"http/client/sent_bytes",
			"Total bytes sent in request body (not including headers)",
			stats.UnitBytes),
		clientReceivedBytes: stats.Int64(
			"http/client/received_bytes",
			"Total bytes received in response bodies (not including headers but including error responses with bodies)",
			stats.UnitBytes),
		clientRoundtripLatency: stats.Float64(
			"http/client/roundtrip_latency",
			"Time between first byte of request headers sent to last byte of response received, or terminal error",
			stats.UnitMilliseconds),
		clientCompletedCount: stats.Int64(
			"http/client/completed_count",
			"Count of completed requests",
			stats.UnitDimensionless),

		enabled: false,
	}
}

func (h *httpMetrics) IsEnabled() bool {
	return h.enabled
}

func (h *httpMetrics) ServerRequestReceived(ctx context.Context, method, path string, contentSize int64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, path, httpMethodKey, method),
			h.serverRequestCount.M(1))
		stats.RecordWithTags(
			ctx, diag_utils.WithTags(appIDKey, h.appID),
			h.serverRequestBytes.M(contentSize))
	}
}

func (h *httpMetrics) ServerRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, path, httpMethodKey, method, httpStatusCodeKey, status),
			h.serverResponseCount.M(1))
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, path, httpMethodKey, method, httpStatusCodeKey, status),
			h.serverLatency.M(elapsed))
		stats.RecordWithTags(
			ctx, diag_utils.WithTags(appIDKey, h.appID),
			h.serverResponseBytes.M(contentSize))
	}
}

func (h *httpMetrics) ClientRequestStarted(ctx context.Context, method, path string, contentSize int64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method),
			h.clientSentBytes.M(contentSize))
	}
}

func (h *httpMetrics) ClientRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method, httpStatusCodeKey, status),
			h.clientCompletedCount.M(1))
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method, httpStatusCodeKey, status),
			h.clientRoundtripLatency.M(elapsed))
		stats.RecordWithTags(
			ctx, diag_utils.WithTags(appIDKey, h.appID),
			h.clientReceivedBytes.M(contentSize))
	}
}

func (h *httpMetrics) Init(appID string) error {
	h.appID = appID
	h.enabled = true

	tags := []tag.Key{appIDKey}
	return view.Register(
		diag_utils.NewMeasureView(h.serverRequestCount, []tag.Key{appIDKey, httpPathKey, httpMethodKey}, view.Count()),
		diag_utils.NewMeasureView(h.serverRequestBytes, tags, defaultSizeDistribution),
		diag_utils.NewMeasureView(h.serverResponseBytes, tags, defaultSizeDistribution),
		diag_utils.NewMeasureView(h.serverLatency, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(h.serverResponseCount, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, view.Count()),
		diag_utils.NewMeasureView(h.clientSentBytes, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, defaultSizeDistribution),
		diag_utils.NewMeasureView(h.clientReceivedBytes, tags, defaultSizeDistribution),
		diag_utils.NewMeasureView(h.clientRoundtripLatency, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(h.clientCompletedCount, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, view.Count()),
	)
}

// FastHTTPMiddleware is the middleware to track http server-side requests.
func (h *httpMetrics) FastHTTPMiddleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		reqContentSize := ctx.Request.Header.ContentLength()
		if reqContentSize < 0 {
			reqContentSize = 0
		}

		method := string(ctx.Method())
		path := h.convertPathToMetricLabel(string(ctx.Path()))

		h.ServerRequestReceived(ctx, method, path, int64(reqContentSize))

		start := time.Now()

		next(ctx)

		status := strconv.Itoa(ctx.Response.StatusCode())
		elapsed := float64(time.Since(start) / time.Millisecond)
		respSize := int64(len(ctx.Response.Body()))
		h.ServerRequestCompleted(ctx, method, path, status, respSize, elapsed)
	}
}

// convertPathToMetricLabel removes the variant parameters in URL path for low cardinality label space
// For example, it removes {keys} param from /v1/state/statestore/{keys}.
func (h *httpMetrics) convertPathToMetricLabel(path string) string {
	if path == "" {
		return path
	}

	p := path
	if p[0] == '/' {
		p = path[1:]
	}

	// Split up to 6 delimiters in 'v1/actors/DemoActor/1/timer/name'
	parsedPath := strings.SplitN(p, "/", 6)

	if len(parsedPath) < 3 {
		return path
	}

	// Replace actor id with {id} for appcallback url - 'actors/DemoActor/1/method/method1'
	if parsedPath[0] == "actors" {
		parsedPath[2] = "{id}"
		return strings.Join(parsedPath, "/")
	}

	switch parsedPath[1] {
	case "state", "secrets":
		// state api: Concat 3 items(v1, state, statestore) in /v1/state/statestore/key
		// secrets api: Concat 3 items(v1, secrets, keyvault) in /v1/secrets/keyvault/name
		return "/" + strings.Join(parsedPath[0:3], "/")

	case "actors":
		if len(parsedPath) < 5 {
			return path
		}
		// ignore id part
		parsedPath[3] = "{id}"
		// Concat 5 items(v1, actors, DemoActor, {id}, timer) in /v1/actors/DemoActor/1/timer/name
		return "/" + strings.Join(parsedPath[0:5], "/")
	}

	return path
}
