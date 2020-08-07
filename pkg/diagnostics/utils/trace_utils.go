// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"context"
	"strconv"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
)

const (
	defaultSamplingRate = 1e-4

	// daprFastHTTPContextKey is the context value of span in fasthttp.RequestCtx.
	daprFastHTTPContextKey = "daprSpanContextKey"
)

// StdoutExporter is an open census exporter that writes to stdout.
type StdoutExporter struct{}

var _ trace.Exporter = &StdoutExporter{}

var log = logger.NewLogger("dapr.runtime.trace")

const msg = "[%s] Trace: %s Span: %s/%s Time: [%s ->  %s] Annotations: %+v"

// ExportSpan implements the open census exporter interface
func (e *StdoutExporter) ExportSpan(sd *trace.SpanData) {
	log.Infof(msg, sd.Name, sd.TraceID, sd.ParentSpanID, sd.SpanID, sd.StartTime, sd.EndTime, sd.Annotations)
}

// GetTraceSamplingRate parses the given rate and returns the parsed rate
func GetTraceSamplingRate(rate string) float64 {
	f, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		return defaultSamplingRate
	}
	return f
}

// TraceSampler returns Probability Sampler option.
func TraceSampler(samplingRate string) trace.StartOption {
	return trace.WithSampler(trace.ProbabilitySampler(GetTraceSamplingRate(samplingRate)))
}

// IsTracingEnabled parses the given rate and returns false if sampling rate is explicitly set 0
func IsTracingEnabled(rate string) bool {
	f, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		// tracing will be considered enabled with default sampling rate even if there is error in parsing
		return true
	}
	return f != 0
}

// SpanFromContext returns the SpanContext stored in a context, or nil if there isn't one.
func SpanFromContext(ctx context.Context) *trace.Span {
	if reqCtx, ok := ctx.(*fasthttp.RequestCtx); ok {
		val := reqCtx.UserValue(daprFastHTTPContextKey)
		if val == nil {
			return nil
		}
		return val.(*trace.Span)
	}

	return trace.FromContext(ctx)
}

// SpanToFastHTTPContext sets span into fasthttp.RequestCtx.
func SpanToFastHTTPContext(ctx *fasthttp.RequestCtx, span *trace.Span) {
	ctx.SetUserValue(daprFastHTTPContextKey, span)
}
