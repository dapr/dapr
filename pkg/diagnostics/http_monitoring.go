// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"strconv"
	"time"

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

	appID string
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
	}
}

func (h *httpMetrics) ServerRequestReceived(ctx context.Context, method, path string, contentSize int64) {
	stats.RecordWithTags(
		ctx,
		tagMutatorsWithAppID(h.appID, tag.Upsert(httpPathKey, path), tag.Upsert(httpMethodKey, method)),
		h.serverRequestCount.M(1))
	stats.RecordWithTags(
		ctx,
		tagMutatorsWithAppID(h.appID),
		h.serverRequestBytes.M(contentSize))
}

func (h *httpMetrics) ServerResponsed(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	stats.RecordWithTags(
		ctx,
		tagMutatorsWithAppID(h.appID, tag.Upsert(httpPathKey, path), tag.Upsert(httpMethodKey, method), tag.Upsert(httpStatusCodeKey, status)),
		h.serverLatency.M(elapsed))

	stats.RecordWithTags(
		ctx,
		tagMutatorsWithAppID(h.appID),
		h.serverResponseBytes.M(contentSize))
}

func (h *httpMetrics) Init(appID string) error {
	h.appID = appID

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
			TagKeys:     []tag.Key{appIDKey},
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
	}

	return view.Register(views...)
}

// FastHTTPMiddleware is the middleware to track http server-side requests
func (h *httpMetrics) FastHTTPMiddleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		var reqContentSize int64 = 0
		if ctx.Request.Header.ContentLength() != -1 {
			reqContentSize = int64(ctx.Request.Header.ContentLength())
		}
		method := string(ctx.Method())
		path := string(ctx.Path())
		h.ServerRequestReceived(ctx, method, path, reqContentSize)

		start := time.Now()

		next(ctx)

		status := strconv.Itoa(ctx.Response.StatusCode())
		elapsed := float64(time.Since(start) / time.Millisecond)
		respSize := int64(len(ctx.Response.Body()))

		h.ServerResponsed(ctx, method, path, status, respSize, elapsed)
	}
}
