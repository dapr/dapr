/*
Copyright 2021 The Dapr Authors
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
	"strings"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/utils/responsewriter"
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

	healthProbeCompletedCount  *stats.Int64Measure
	healthProbeRoundripLatency *stats.Float64Measure

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
	return h.enabled
}

func (h *httpMetrics) ServerRequestReceived(ctx context.Context, method, path string, contentSize int64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.serverRequestCount.Name(), appIDKey, h.appID, httpPathKey, path, httpMethodKey, method),
			h.serverRequestCount.M(1))
		stats.RecordWithTags(
			ctx, diagUtils.WithTags(h.serverRequestBytes.Name(), appIDKey, h.appID),
			h.serverRequestBytes.M(contentSize))
	}
}

func (h *httpMetrics) ServerRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.serverResponseCount.Name(), appIDKey, h.appID, httpPathKey, path, httpMethodKey, method, httpStatusCodeKey, status),
			h.serverResponseCount.M(1))
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.serverLatency.Name(), appIDKey, h.appID, httpPathKey, path, httpMethodKey, method, httpStatusCodeKey, status),
			h.serverLatency.M(elapsed))
		stats.RecordWithTags(
			ctx, diagUtils.WithTags(h.serverResponseBytes.Name(), appIDKey, h.appID),
			h.serverResponseBytes.M(contentSize))
	}
}

func (h *httpMetrics) ClientRequestStarted(ctx context.Context, method, path string, contentSize int64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientSentBytes.Name(), appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method),
			h.clientSentBytes.M(contentSize))
	}
}

func (h *httpMetrics) ClientRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientCompletedCount.Name(), appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method, httpStatusCodeKey, status),
			h.clientCompletedCount.M(1))
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.clientRoundtripLatency.Name(), appIDKey, h.appID, httpPathKey, h.convertPathToMetricLabel(path), httpMethodKey, method, httpStatusCodeKey, status),
			h.clientRoundtripLatency.M(elapsed))
		stats.RecordWithTags(
			ctx, diagUtils.WithTags(h.clientReceivedBytes.Name(), appIDKey, h.appID),
			h.clientReceivedBytes.M(contentSize))
	}
}

func (h *httpMetrics) AppHealthProbeStarted(ctx context.Context) {
	if h.enabled {
		stats.RecordWithTags(ctx, diagUtils.WithTags("", appIDKey, h.appID))
	}
}

func (h *httpMetrics) AppHealthProbeCompleted(ctx context.Context, status string, elapsed float64) {
	if h.enabled {
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.healthProbeCompletedCount.Name(), appIDKey, h.appID, httpStatusCodeKey, status),
			h.healthProbeCompletedCount.M(1))
		stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(h.healthProbeRoundripLatency.Name(), appIDKey, h.appID, httpStatusCodeKey, status),
			h.healthProbeRoundripLatency.M(elapsed))
	}
}

func (h *httpMetrics) Init(appID string) error {
	h.appID = appID
	h.enabled = true

	tags := []tag.Key{appIDKey}
	return view.Register(
		diagUtils.NewMeasureView(h.serverRequestCount, []tag.Key{appIDKey, httpPathKey, httpMethodKey}, view.Count()),
		diagUtils.NewMeasureView(h.serverRequestBytes, tags, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.serverResponseBytes, tags, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.serverLatency, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(h.serverResponseCount, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, view.Count()),
		diagUtils.NewMeasureView(h.clientSentBytes, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.clientReceivedBytes, tags, defaultSizeDistribution),
		diagUtils.NewMeasureView(h.clientRoundtripLatency, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(h.clientCompletedCount, []tag.Key{appIDKey, httpMethodKey, httpPathKey, httpStatusCodeKey}, view.Count()),
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

		path := h.convertPathToMetricLabel(r.URL.Path)

		h.ServerRequestReceived(r.Context(), r.Method, path, reqContentSize)

		// Wrap the writer in a ResponseWriter so we can collect stats such as status code and size
		w = responsewriter.EnsureResponseWriter(w)

		start := time.Now()

		next.ServeHTTP(w, r)

		elapsed := float64(time.Since(start) / time.Millisecond)
		status := strconv.Itoa(w.(responsewriter.ResponseWriter).Status())
		respSize := int64(w.(responsewriter.ResponseWriter).Size())
		h.ServerRequestCompleted(r.Context(), r.Method, path, status, respSize, elapsed)
	})
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
