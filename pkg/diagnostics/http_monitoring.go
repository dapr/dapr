/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package diagnostics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/responsewriter"
)

// To track the metrics for fasthttp using opencensus, this implementation is inspired by
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ochttp

// Tag key definitions for http requests.
var (
	httpStatusCodeKey = tag.MustNewKey("status")
	httpPathKey       = tag.MustNewKey("path")
	httpMethodKey     = tag.MustNewKey("method")
)

var (
	// <<10 -> KBs; <<20 -> MBs; <<30 -> GBs
	defaultSizeDistribution    = view.Distribution(1<<10, 2<<10, 4<<10, 16<<10, 64<<10, 256<<10, 1<<20, 4<<20, 16<<20, 64<<20, 256<<20, 1<<30, 4<<30)
	defaultLatencyDistribution = view.Distribution(1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1_000, 2_000, 5_000, 10_000, 20_000, 50_000, 100_000)
)

type httpMetrics struct {
	serverRequestBytes  *stats.Int64Measure
	serverResponseBytes *stats.Int64Measure
	serverLatency       *stats.Float64Measure
	serverRequestCount  *stats.Int64Measure

	clientSentBytes        *stats.Int64Measure
	clientReceivedBytes    *stats.Int64Measure
	clientRoundtripLatency *stats.Float64Measure
	clientCompletedCount   *stats.Int64Measure

	healthProbeCompletedCount  *stats.Int64Measure
	healthProbeRoundripLatency *stats.Float64Measure

	appID   string
	enabled bool

	// Enable legacy metrics, which includes the full path
	legacy bool
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{
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
			"HTTP request end-to-end latency in server.",
			stats.UnitMilliseconds),
		serverRequestCount: stats.Int64(
			"http/server/request_count",
			"Count of HTTP requests processed by the server.",
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
		healthProbeCompletedCount: stats.Int64(
			"http/healthprobes/completed_count",
			"Count of completed health probes",
			stats.UnitDimensionless),
		healthProbeRoundripLatency: stats.Float64(
			"http/healthprobes/roundtrip_latency",
			"Time between first byte of health probes headers sent to last byte of response received, or terminal error",
			stats.UnitMilliseconds),

		enabled: false,
	}
}

func (h *httpMetrics) IsEnabled() bool {
	return h != nil && h.enabled
}

func (h *httpMetrics) ServerRequestCompleted(ctx context.Context, method, path, status string, reqContentSize, resContentSize int64, elapsed float64) {
	if !h.IsEnabled() {
		return
	}

	if h.legacy {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.serverRequestCount.Name(), appIDKey, h.appID, httpMethodKey, method, httpPathKey, path, httpStatusCodeKey, status),
			h.serverRequestCount.M(1))
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.serverLatency.Name(), appIDKey, h.appID, httpMethodKey, method, httpPathKey, path, httpStatusCodeKey, status),
			h.serverLatency.M(elapsed))
	} else {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.serverRequestCount.Name(), appIDKey, h.appID, httpMethodKey, method, httpStatusCodeKey, status),
			h.serverRequestCount.M(1))
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.serverLatency.Name(), appIDKey, h.appID, httpMethodKey, method, httpStatusCodeKey, status),
			h.serverLatency.M(elapsed))
	}
	stats.RecordWithTags(
		ctx, diagUtils.WithTags(h.serverRequestBytes.Name(), appIDKey, h.appID),
		h.serverRequestBytes.M(reqContentSize))
	stats.RecordWithTags(
		ctx, diagUtils.WithTags(h.serverResponseBytes.Name(), appIDKey, h.appID),
		h.serverResponseBytes.M(resContentSize))
}

func (h *httpMetrics) ClientRequestStarted(ctx context.Context, method, path string, contentSize int64) {
	if !h.IsEnabled() {
		return
	}

	if h.legacy {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientSentBytes.Name(), appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method),
			h.clientSentBytes.M(contentSize))
	} else {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientSentBytes.Name(), appIDKey, h.appID),
			h.clientSentBytes.M(contentSize))
	}
}

func (h *httpMetrics) ClientRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if !h.IsEnabled() {
		return
	}

	if h.legacy {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientCompletedCount.Name(), appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method, httpStatusCodeKey, status),
			h.clientCompletedCount.M(1))
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientRoundtripLatency.Name(), appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method, httpStatusCodeKey, status),
			h.clientRoundtripLatency.M(elapsed))
	} else {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientCompletedCount.Name(), appIDKey, h.appID, httpStatusCodeKey, status),
			h.clientCompletedCount.M(1))
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientRoundtripLatency.Name(), appIDKey, h.appID, httpStatusCodeKey, status),
			h.clientRoundtripLatency.M(elapsed))
	}
	stats.RecordWithTags(
		ctx, diagUtils.WithTags(h.clientReceivedBytes.Name(), appIDKey, h.appID),
		h.clientReceivedBytes.M(contentSize))
}

func (h *httpMetrics) AppHealthProbeStarted(ctx context.Context) {
	if !h.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags("", appIDKey, h.appID))
}

func (h *httpMetrics) AppHealthProbeCompleted(ctx context.Context, status string, elapsed float64) {
	if !h.IsEnabled() {
		return
	}

	stats.RecordWithTags(
		ctx,
		diagUtils.WithTags(h.healthProbeCompletedCount.Name(), appIDKey, h.appID, httpStatusCodeKey, status),
		h.healthProbeCompletedCount.M(1))
	stats.RecordWithTags(
		ctx,
		diagUtils.WithTags(h.healthProbeRoundripLatency.Name(), appIDKey, h.appID, httpStatusCodeKey, status),
		h.healthProbeRoundripLatency.M(elapsed))
}

func (h *httpMetrics) Init(appID string, legacy bool) error {
	h.appID = appID
	h.enabled = true
	h.legacy = legacy

	tags := []tag.Key{appIDKey}

	// In legacy mode, we are aggregating based on the path too
	var serverTags, clientTags []tag.Key
	if h.legacy {
		serverTags = []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}
		clientTags = []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}
	} else {
		serverTags = []tag.Key{appIDKey, httpMethodKey, httpStatusCodeKey}
		clientTags = []tag.Key{appIDKey, httpStatusCodeKey}
	}
	return view.Register(
		diagUtils.NewMeasureView(h.serverRequestBytes, tags, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.serverResponseBytes, tags, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.serverLatency, serverTags, defaultLatencyDistribution),
		diagUtils.NewMeasureView(h.serverRequestCount, serverTags, view.Count()),
		diagUtils.NewMeasureView(h.clientSentBytes, clientTags, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.clientReceivedBytes, tags, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.clientRoundtripLatency, clientTags, defaultLatencyDistribution),
		diagUtils.NewMeasureView(h.clientCompletedCount, clientTags, view.Count()),
		diagUtils.NewMeasureView(h.healthProbeRoundripLatency, []tag.Key{appIDKey, httpStatusCodeKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(h.healthProbeCompletedCount, []tag.Key{appIDKey, httpStatusCodeKey}, view.Count()),
	)
}

// HTTPMiddleware is the middleware to track HTTP server-side requests.
func (h *httpMetrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqContentSize int64
		if cl := r.Header.Get("content-length"); cl != "" {
			reqContentSize, _ = strconv.ParseInt(cl, 10, 64)
			if reqContentSize < 0 {
				reqContentSize = 0
			}
		}

		var path string
		if h.legacy {
			path = h.convertPathToMetricLabel(r.URL.Path)
		}

		// Wrap the writer in a ResponseWriter so we can collect stats such as status code and size
		rw := responsewriter.EnsureResponseWriter(w)

		// Process the request
		start := time.Now()
		next.ServeHTTP(rw, r)

		elapsed := float64(time.Since(start) / time.Millisecond)
		status := strconv.Itoa(rw.Status())
		respSize := int64(rw.Size())

		var method string
		if h.legacy {
			method = r.Method
		} else {
			// Check if the context contains a MethodName method
			endpointData, _ := r.Context().Value(endpoints.EndpointCtxKey{}).(*endpoints.EndpointCtxData)
			method = endpointData.GetEndpointName()
			if endpointData != nil && endpointData.Group != nil && endpointData.Group.MethodName != nil {
				method = endpointData.Group.MethodName(r)
			}
		}

		// Record the request
		h.ServerRequestCompleted(r.Context(), method, path, status, reqContentSize, respSize, elapsed)
	})
}
