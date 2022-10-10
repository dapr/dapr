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
	"strconv"
	"strings"
	"time"

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"

	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncfloat64"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	semconv "go.opentelemetry.io/otel/semconv/v1.9.0"
)

type httpMetrics struct {
	serverRequestCount  syncint64.Counter
	serverRequestBytes  syncint64.Histogram
	serverResponseBytes syncint64.Histogram
	serverLatency       syncfloat64.Histogram
	serverResponseCount syncint64.Counter

	clientSentBytes        syncint64.Histogram
	clientReceivedBytes    syncint64.Histogram
	clientRoundtripLatency syncfloat64.Histogram
	clientCompletedCount   syncint64.Counter

	healthProbeCompletedCount  syncint64.Counter
	healthProbeRoundripLatency syncfloat64.Histogram
}

func (m *MetricClient) newHTTPMetrics() *httpMetrics {
	hm := new(httpMetrics)
	hm.serverRequestCount, _ = m.meter.SyncInt64().Counter(
		"http/server/request_count",
		instrument.WithDescription("Number of HTTP requests started in server."),
		instrument.WithUnit(unit.Dimensionless))
	hm.serverRequestBytes, _ = m.meter.SyncInt64().Histogram(
		"http/server/request_bytes",
		instrument.WithDescription("HTTP request body size if set as ContentLength (uncompressed) in server."),
		instrument.WithUnit(unit.Bytes))
	hm.serverResponseBytes, _ = m.meter.SyncInt64().Histogram(
		"http/server/response_bytes",
		instrument.WithDescription("HTTP response body size (uncompressed) in server."),
		instrument.WithUnit(unit.Bytes))
	hm.serverLatency, _ = m.meter.SyncFloat64().Histogram(
		"http/server/latency",
		instrument.WithDescription("HTTP request end to end latency in server."),
		instrument.WithUnit(unit.Milliseconds))
	hm.serverResponseCount, _ = m.meter.SyncInt64().Counter(
		"http/server/response_count",
		instrument.WithDescription("The number of HTTP responses"),
		instrument.WithUnit(unit.Dimensionless))
	hm.clientSentBytes, _ = m.meter.SyncInt64().Histogram(
		"http/client/sent_bytes",
		instrument.WithDescription("Total bytes sent in request body (not including headers)"),
		instrument.WithUnit(unit.Bytes))
	hm.clientReceivedBytes, _ = m.meter.SyncInt64().Histogram(
		"http/client/received_bytes",
		instrument.WithDescription("Total bytes received in response bodies (not including headers but including error responses with bodies)"),
		instrument.WithUnit(unit.Bytes))
	hm.clientRoundtripLatency, _ = m.meter.SyncFloat64().Histogram(
		"http/client/roundtrip_latency",
		instrument.WithDescription("Time between first byte of request headers sent to last byte of response received, or terminal error"),
		instrument.WithUnit(unit.Milliseconds))
	hm.clientCompletedCount, _ = m.meter.SyncInt64().Counter(
		"http/client/completed_count",
		instrument.WithDescription("Count of completed requests"),
		instrument.WithUnit(unit.Dimensionless))

	hm.healthProbeCompletedCount, _ = m.meter.SyncInt64().Counter(
		"http/healthprobes/completed_count",
		instrument.WithDescription("Count of completed health probes"),
		instrument.WithUnit(unit.Dimensionless))
	hm.healthProbeRoundripLatency, _ = m.meter.SyncFloat64().Histogram(
		"http/healthprobes/roundtrip_latency",
		instrument.WithDescription("Time between first byte of health probes headers sent to last byte of response received, or terminal error"),
		instrument.WithUnit(unit.Milliseconds))

	return hm
}

func (h *httpMetrics) ServerRequestReceived(ctx context.Context, method, path string, contentSize int64) {
	if h == nil {
		return
	}
	attributes := []attribute.KeyValue{
		semconv.HTTPMethodKey.String(method),
		semconv.HTTPTargetKey.String(path),
	}
	h.serverRequestCount.Add(ctx, 1, attributes...)
	h.serverRequestBytes.Record(ctx, contentSize, attributes...)
}

func (h *httpMetrics) ServerRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if h == nil {
		return
	}
	attributes := []attribute.KeyValue{
		semconv.HTTPMethodKey.String(method),
		semconv.HTTPTargetKey.String(path),
		semconv.HTTPStatusCodeKey.String(status),
	}
	h.serverResponseCount.Add(ctx, 1, attributes...)
	h.serverLatency.Record(ctx, elapsed, attributes...)
	h.serverResponseBytes.Record(ctx, contentSize, attributes[:len(attributes)-1]...)
}

func (h *httpMetrics) ClientRequestStarted(ctx context.Context, method, path string, contentSize int64) {
	if h == nil {
		return
	}
	path = h.convertPathToMetricLabel(path)
	attributes := []attribute.KeyValue{
		semconv.HTTPMethodKey.String(method),
		semconv.HTTPTargetKey.String(path),
	}
	h.clientSentBytes.Record(ctx, contentSize, attributes...)
}

func (h *httpMetrics) ClientRequestCompleted(ctx context.Context, method, path, status string, contentSize int64, elapsed float64) {
	if h == nil {
		return
	}
	path = h.convertPathToMetricLabel(path)
	attributes := []attribute.KeyValue{
		semconv.HTTPMethodKey.String(method),
		semconv.HTTPTargetKey.String(path),
		semconv.HTTPStatusCodeKey.String(status),
	}
	h.clientCompletedCount.Add(ctx, 1, attributes...)
	h.clientRoundtripLatency.Record(ctx, elapsed, attributes...)
	h.clientReceivedBytes.Record(ctx, contentSize, attributes[:len(attributes)-1]...)
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

func (h *httpMetrics) AppHealthProbeCompleted(ctx context.Context, status string, start time.Time) {
	if h == nil {
		return
	}

	elapsed := float64(time.Since(start) / time.Millisecond)
	attributes := []attribute.KeyValue{
		semconv.RPCSystemKey.String("http"),
		isemconv.RPCTypeClient,
		isemconv.RPCStatusKey.String(status),
	}
	h.healthProbeCompletedCount.Add(ctx, 1, attributes...)
	h.healthProbeRoundripLatency.Record(ctx, elapsed, attributes...)

	return
}
