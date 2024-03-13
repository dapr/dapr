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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/responsewriter"
)

// To track the metrics for fasthttp using opencensus, this implementation is inspired by
// https://github.com/census-instrumentation/opencensus-go/tree/master/plugin/ochttp

// Tag key definitions for http requests.
var (
	httpStatusCodeKey = "status"
	httpPathKey       = "path"
	httpMethodKey     = "method"
)

var (
// <<10 -> KBs; <<20 -> MBs; <<30 -> GBs
// defaultSizeDistribution    = view.Distribution(1<<10, 2<<10, 4<<10, 16<<10, 64<<10, 256<<10, 1<<20, 4<<20, 16<<20, 64<<20, 256<<20, 1<<30, 4<<30)
// defaultLatencyDistribution = view.Distribution(1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1_000, 2_000, 5_000, 10_000, 20_000, 50_000, 100_000)
)

type httpMetrics struct {
	meter metric.Meter

	serverRequestBytes  metric.Int64Counter
	serverResponseBytes metric.Int64Counter
	serverLatency       metric.Float64Histogram
	serverRequestCount  metric.Int64Counter

	clientSentBytes        metric.Int64Counter
	clientReceivedBytes    metric.Int64Counter
	clientRoundtripLatency metric.Float64Histogram
	clientCompletedCount   metric.Int64Counter

	healthProbeCompletedCount  metric.Int64Counter
	healthProbeRoundripLatency metric.Float64Histogram

	appID   string
	enabled bool

	// Enable legacy metrics, which includes the full path
	legacy bool
}

func newHTTPMetrics() *httpMetrics {
	m := otel.Meter("http")

	return &httpMetrics{
		meter:   m,
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
		h.serverRequestCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpMethodKey, method), attribute.String(httpPathKey, path), attribute.String(httpStatusCodeKey, status)))
		h.serverLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpMethodKey, method), attribute.String(httpPathKey, path), attribute.String(httpStatusCodeKey, status)))
	} else {
		h.serverRequestCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpMethodKey, method), attribute.String(httpStatusCodeKey, status)))
		h.serverLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpStatusCodeKey, status)))
	}
	h.serverRequestBytes.Add(ctx, reqContentSize, metric.WithAttributes(attribute.String(appIDKey, h.appID)))
	h.serverResponseBytes.Add(ctx, resContentSize, metric.WithAttributes(attribute.String(appIDKey, h.appID)))
}

func (h *httpMetrics) ClientRequestStarted(ctx context.Context, method, path string, contentSize int64) {
	if !h.IsEnabled() {
		return
	}

	if h.legacy {
		h.clientSentBytes.Add(ctx, contentSize, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpMethodKey, method), attribute.String(httpPathKey, h.convertPathToMetricLabel(path))))
	} else {
		h.clientSentBytes.Add(ctx, contentSize, metric.WithAttributes(attribute.String(appIDKey, h.appID)))
	}
}

func (h *httpMetrics) ClientRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if !h.IsEnabled() {
		return
	}

	if h.legacy {
		h.clientCompletedCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpMethodKey, method), attribute.String(httpPathKey, h.convertPathToMetricLabel(path)), attribute.String(httpStatusCodeKey, status)))
		h.clientRoundtripLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpMethodKey, method), attribute.String(httpPathKey, h.convertPathToMetricLabel(path)), attribute.String(httpStatusCodeKey, status)))
	} else {
		h.clientCompletedCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpMethodKey, method), attribute.String(httpStatusCodeKey, status)))
		h.clientRoundtripLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpStatusCodeKey, status)))
	}
	h.clientReceivedBytes.Add(ctx, contentSize, metric.WithAttributes(attribute.String(appIDKey, h.appID)))
}

func (h *httpMetrics) AppHealthProbeStarted(ctx context.Context) {
	if !h.IsEnabled() {
		return
	}

	// TODO?
}

func (h *httpMetrics) AppHealthProbeCompleted(ctx context.Context, status string, elapsed float64) {
	if !h.IsEnabled() {
		return
	}

	h.healthProbeCompletedCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpStatusCodeKey, status)))
	h.healthProbeRoundripLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, h.appID), attribute.String(httpStatusCodeKey, status)))
}

func (h *httpMetrics) Init(appID string, legacy bool) error {
	serverRequestBytes, err := h.meter.Int64Counter(
		"http.server.request_bytes",
		metric.WithDescription("HTTP request body size if set as ContentLength (uncompressed) in server."),
	)
	if err != nil {
		return err
	}

	serverResponseBytes, err := h.meter.Int64Counter(
		"http.server.response_bytes",
		metric.WithDescription("HTTP response body size (uncompressed) in server."),
	)
	if err != nil {
		return err
	}

	serverLatency, err := h.meter.Float64Histogram(
		"http.server.latency",
		metric.WithDescription("HTTP request end-to-end latency in server."),
	)
	if err != nil {
		return err
	}

	serverRequestCount, err := h.meter.Int64Counter(
		"http.server.request_count",
		metric.WithDescription("Count of HTTP requests processed by the server."),
	)
	if err != nil {
		return err
	}

	clientSentBytes, err := h.meter.Int64Counter(
		"http.client.sent_bytes",
		metric.WithDescription("Total bytes sent in request body (not including headers)"),
	)
	if err != nil {
		return err
	}

	clientReceivedBytes, err := h.meter.Int64Counter(
		"http.client.received_bytes",
		metric.WithDescription("Total bytes received in response bodies (not including headers but including error responses with bodies)"),
	)
	if err != nil {
		return err
	}

	clientRoundtripLatency, err := h.meter.Float64Histogram(
		"http.client.roundtrip_latency",
		metric.WithDescription("Time between first byte of request headers sent to last byte of response received, or terminal error"),
	)
	if err != nil {
		return err
	}

	clientCompletedCount, err := h.meter.Int64Counter(
		"http.client.completed_count",
		metric.WithDescription("Count of completed requests"),
	)
	if err != nil {
		return err
	}

	healthProbeCompletedCount, err := h.meter.Int64Counter(
		"http.healthprobes.completed_count",
		metric.WithDescription("Count of completed health probes"),
	)
	if err != nil {
		return err
	}

	healthProbeRoundripLatency, err := h.meter.Float64Histogram(
		"http.healthprobes.roundtrip_latency",
		metric.WithDescription("Time between first byte of health probes headers sent to last byte of response received, or terminal error"),
	)
	if err != nil {
		return err
	}

	h.serverResponseBytes = serverResponseBytes
	h.serverRequestBytes = serverRequestBytes
	h.serverLatency = serverLatency
	h.serverRequestCount = serverRequestCount
	h.clientSentBytes = clientSentBytes
	h.clientReceivedBytes = clientReceivedBytes
	h.clientRoundtripLatency = clientRoundtripLatency
	h.clientCompletedCount = clientCompletedCount
	h.healthProbeCompletedCount = healthProbeCompletedCount
	h.healthProbeRoundripLatency = healthProbeRoundripLatency
	h.appID = appID
	h.enabled = true
	h.legacy = legacy

	return nil
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
