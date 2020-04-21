// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"strconv"
	"time"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

// To track the metrics for fasthttp using opencensus, this implementation is inspired by
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ochttp

// Tag key definitions for http requests
var (
	httpStatusCodeKey = tag.MustNewKey("status")
	httpPathKey       = tag.MustNewKey("path")
	httpMethodKey     = tag.MustNewKey("method")
)

// Default distributions
var (
	defaultSizeDistribution    = view.Distribution(1024, 2048, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824, 4294967296)
	defaultLatencyDistribution = view.Distribution(1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1000, 2000, 5000, 10000, 20000, 50000, 100000)
)

type httpMetrics struct {
	serverRequestCount  *stats.Int64Measure
	serverRequestBytes  *stats.Int64Measure
	serverResponseBytes *stats.Int64Measure
	serverLatency       *stats.Float64Measure

	clientSentBytes        *stats.Int64Measure
	clientReceivedBytes    *stats.Int64Measure
	clientRoundtripLatency *stats.Float64Measure

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

		enabled: false,
	}
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
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, path, httpMethodKey, method),
			h.clientSentBytes.M(contentSize))
	}
}

func (h *httpMetrics) ClientRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, h.appID, httpPathKey, path, httpMethodKey, method, httpStatusCodeKey, status),
			h.clientRoundtripLatency.M(elapsed))
		stats.RecordWithTags(
			ctx, diag_utils.WithTags(appIDKey, h.appID),
			h.clientReceivedBytes.M(contentSize))
	}
}

func (h *httpMetrics) Init(appID string) error {
	h.appID = appID
	h.enabled = true

	views := []*view.View{
		{
			Name:        "http/server/request_count",
			Description: "The Number of HTTP requests",
			TagKeys:     []tag.Key{appIDKey, httpPathKey, httpMethodKey},
			Measure:     h.serverRequestCount,
			Aggregation: view.Count(),
		},
		{
			Name:        "http/server/request_bytes",
			Description: "Size distribution of HTTP request body",
			TagKeys:     []tag.Key{appIDKey},
			Measure:     h.serverRequestBytes,
			Aggregation: defaultSizeDistribution,
		},
		{
			Name:        "http/server/response_bytes",
			Description: "Size distribution of HTTP response body",
			TagKeys:     []tag.Key{appIDKey},
			Measure:     h.serverResponseBytes,
			Aggregation: defaultSizeDistribution,
		},
		{
			Name:        "http/server/latency",
			Description: "Latency distribution of HTTP requests",
			TagKeys:     []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey},
			Measure:     h.serverLatency,
			Aggregation: defaultLatencyDistribution,
		},
		{
			Name:        "http/server/response_count",
			Description: "The number of HTTP responses",
			TagKeys:     []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey},
			Measure:     h.serverLatency,
			Aggregation: view.Count(),
		},

		{
			Name:        "http/client/sent_bytes",
			Measure:     h.clientSentBytes,
			Aggregation: defaultSizeDistribution,
			Description: "Total bytes sent in request body (not including headers)",
			TagKeys:     []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey},
		},
		{
			Name:        "http/client/received_bytes",
			Measure:     h.clientReceivedBytes,
			Aggregation: defaultSizeDistribution,
			Description: "Total bytes received in response bodies (not including headers but including error responses with bodies)",
			TagKeys:     []tag.Key{appIDKey},
		},
		{
			Name:        "http/client/roundtrip_latency",
			Measure:     h.clientRoundtripLatency,
			Aggregation: defaultLatencyDistribution,
			Description: "End-to-end latency",
			TagKeys:     []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey},
		},
		{
			Name:        "http/client/completed_count",
			Measure:     h.clientRoundtripLatency,
			Aggregation: view.Count(),
			Description: "Count of completed requests",
			TagKeys:     []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey},
		},
	}

	return view.Register(views...)
}

// FastHTTPMiddleware is the middleware to track http server-side requests
func (h *httpMetrics) FastHTTPMiddleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		reqContentSize := ctx.Request.Header.ContentLength()
		if reqContentSize < 0 {
			reqContentSize = 0
		}

		method := string(ctx.Method())
		path := string(ctx.Path())

		h.ServerRequestReceived(ctx, method, path, int64(reqContentSize))

		start := time.Now()

		next(ctx)

		status := strconv.Itoa(ctx.Response.StatusCode())
		elapsed := float64(time.Since(start) / time.Millisecond)
		respSize := int64(len(ctx.Response.Body()))
		h.ServerRequestCompleted(ctx, method, path, status, respSize, elapsed)
	}
}
