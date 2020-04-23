// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"go.opencensus.io/trace"
	"google.golang.org/grpc/metadata"
)

type key string

const (
	// CorrelationID is the header key name of correlation id for trace
	CorrelationID        = "X-Correlation-ID"
	correlationKey   key = CorrelationID
	daprHeaderPrefix     = "dapr-"
)

// TracerSpan defines a tracing span that a tracer users to keep track of call scopes
type TracerSpan struct {
	Context     context.Context
	Span        *trace.Span
	SpanContext *trace.SpanContext
}

// SerializeSpanContext serializes a span context into a simple string
func SerializeSpanContext(ctx trace.SpanContext) string {
	return fmt.Sprintf("%s;%s;%d", ctx.SpanID.String(), ctx.TraceID.String(), ctx.TraceOptions)
}

// DeserializeSpanContext deserializes a span context from a string
func DeserializeSpanContext(ctx string) trace.SpanContext {
	parts := strings.Split(ctx, ";")
	spanID, _ := hex.DecodeString(parts[0])
	traceID, _ := hex.DecodeString(parts[1])
	traceOptions, _ := strconv.ParseUint(parts[2], 10, 32)
	ret := trace.SpanContext{}
	copy(ret.SpanID[:], spanID)
	copy(ret.TraceID[:], traceID)
	ret.TraceOptions = trace.TraceOptions(traceOptions)
	return ret
}

func projectStatusCode(code int) int32 {
	switch code {
	case 200:
		return trace.StatusCodeOK
	case 201:
		return trace.StatusCodeOK
	case 400:
		return trace.StatusCodeInvalidArgument
	case 500:
		return trace.StatusCodeInternal
	case 404:
		return trace.StatusCodeNotFound
	case 403:
		return trace.StatusCodePermissionDenied
	default:
		return int32(code)
	}
}

func createSpanName(name string) string {
	i := strings.Index(name, "/invoke/")
	if i > 0 {
		j := strings.Index(name[i+8:], "/")
		if j > 0 {
			return name[i+8 : i+8+j]
		}
	}
	return name
}

func extractDaprMetadata(ctx context.Context) map[string][]string {
	daprMetadata := make(map[string][]string)
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return daprMetadata
	}

	for k, v := range md {
		k = strings.ToLower(k)
		if strings.HasPrefix(k, daprHeaderPrefix) {
			daprMetadata[k] = v
		}
	}

	return daprMetadata
}

// UpdateSpanPairStatusesFromError updates tracer span statuses based on error object
func UpdateSpanPairStatusesFromError(span, spanc TracerSpan, err error, method string) {
	if err != nil {
		spanc.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeInternal,
			Message: fmt.Sprintf("method %s failed - %s", method, err.Error()),
		})
		span.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeInternal,
			Message: fmt.Sprintf("method %s failed - %s", method, err.Error()),
		})
	} else {
		spanc.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeOK,
			Message: fmt.Sprintf("method %s succeeded", method),
		})
		span.Span.SetStatus(trace.Status{
			Code:    trace.StatusCodeOK,
			Message: fmt.Sprintf("method %s succeeded", method),
		})
	}
}
