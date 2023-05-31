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

package utils

import (
	"context"
	"net/http"
	"strconv"

	"github.com/valyala/fasthttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/kit/logger"
)

type daprContextKey string

const (
	defaultSamplingRate = 1e-4

	spanContextKey daprContextKey = "span"
)

var emptySpanContext trace.SpanContext

// StdoutExporter implements an open telemetry span exporter that writes to stdout.
type StdoutExporter struct {
	log logger.Logger
}

// NewStdOutExporter returns a StdOutExporter
func NewStdOutExporter() *StdoutExporter {
	return &StdoutExporter{logger.NewLogger("dapr.runtime.trace")}
}

// ExportSpans implements the open telemetry span exporter interface.
func (e *StdoutExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	const msg = "[%s] Trace: %s Span: %s/%s Time: [%s ->  %s] Annotations: %+v"
	for _, sd := range spans {
		var parentSpanID trace.SpanID
		if sd.Parent().IsValid() {
			parentSpanID = sd.Parent().SpanID()
		}
		e.log.Infof(msg, sd.Name(), sd.SpanContext().TraceID(), parentSpanID, sd.SpanContext().SpanID(), sd.StartTime(), sd.EndTime(), sd.Events())
	}
	return nil
}

// Shutdown implements the open telemetry span exporter interface.
func (e *StdoutExporter) Shutdown(ctx context.Context) error {
	return nil
}

// NullExporter implements an open telemetry span exporter that discards all telemetry.
type NullExporter struct{}

// NewNullExporter returns a NullExporter
func NewNullExporter() *NullExporter {
	return &NullExporter{}
}

// ExportSpans implements the open telemetry span exporter interface.
func (e *NullExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return nil
}

// Shutdown implements the open telemetry span exporter interface.
func (e *NullExporter) Shutdown(ctx context.Context) error {
	return nil
}

// GetTraceSamplingRate parses the given rate and returns the parsed rate.
func GetTraceSamplingRate(rate string) float64 {
	f, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return defaultSamplingRate
	}
	return f
}

// IsTracingEnabled parses the given rate and returns false if sampling rate is explicitly set 0.
func IsTracingEnabled(rate string) bool {
	return GetTraceSamplingRate(rate) != 0
}

// SpanFromContext returns the Span stored in a context, or nil or trace.noopSpan{} if there isn't one.
func SpanFromContext(ctx context.Context) trace.Span {
	// TODO: Remove fasthttp compatibility when no HTTP API using contexts depend on fasthttp
	var val any
	if reqCtx, ok := ctx.(*fasthttp.RequestCtx); ok {
		val = reqCtx.UserValue(spanContextKey)
	} else {
		val = ctx.Value(spanContextKey)
	}

	if val != nil {
		span, ok := val.(trace.Span)
		if ok {
			return span
		}
	}

	// Return the default span, which can be a noop
	return trace.SpanFromContext(ctx)
}

// AddSpanToFasthttpContext adds the span to the fasthttp request context.
// TODO: Remove fasthttp compatibility when no HTTP API using contexts depend on fasthttp.
func AddSpanToFasthttpContext(ctx *fasthttp.RequestCtx, span trace.Span) {
	ctx.SetUserValue(spanContextKey, span)
}

// AddSpanToRequest sets span into a request context.
func AddSpanToRequest(r *http.Request, span trace.Span) {
	ctx := context.WithValue(r.Context(), spanContextKey, span)
	*r = *(r.WithContext(ctx))
}

// BinaryFromSpanContext returns the binary format representation of a SpanContext.
//
// If sc is the zero value, Binary returns nil.
func BinaryFromSpanContext(sc trace.SpanContext) []byte {
	traceID := sc.TraceID()
	spanID := sc.SpanID()
	traceFlags := sc.TraceFlags()
	if sc.Equal(emptySpanContext) {
		return nil
	}
	var b [29]byte
	copy(b[2:18], traceID[:])
	b[18] = 1
	copy(b[19:27], spanID[:])
	b[27] = 2
	b[28] = uint8(traceFlags)
	return b[:]
}

// SpanContextFromBinary returns the SpanContext represented by b.
//
// If b has an unsupported version ID or contains no TraceID, SpanContextFromBinary returns with ok==false.
func SpanContextFromBinary(b []byte) (sc trace.SpanContext, ok bool) {
	var scConfig trace.SpanContextConfig
	if len(b) == 0 || b[0] != 0 {
		return trace.SpanContext{}, false
	}
	b = b[1:]
	if len(b) >= 17 && b[0] == 0 {
		copy(scConfig.TraceID[:], b[1:17])
		b = b[17:]
	} else {
		return trace.SpanContext{}, false
	}
	if len(b) >= 9 && b[0] == 1 {
		copy(scConfig.SpanID[:], b[1:9])
		b = b[9:]
	}
	if len(b) >= 2 && b[0] == 2 {
		scConfig.TraceFlags = trace.TraceFlags(b[1])
	}
	sc = trace.NewSpanContext(scConfig)
	return sc, true
}
