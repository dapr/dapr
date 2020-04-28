// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
)

func TestStartClientSpanTracing(t *testing.T) {
	req := getTestHTTPRequest()
	spec := config.TracingSpec{SamplingRate: "0.5"}

	StartTracingClientSpanFromHTTPContext(context.Background(), req, "test", spec)
}

func TestTracingClientSpanFromHTTPContext(t *testing.T) {
	reqCtx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
	sc := GetSpanContextFromRequestContext(reqCtx)
	ctx := NewContext((context.Context)(reqCtx), sc)
	StartTracingClientSpanFromHTTPContext(ctx, &reqCtx.Request, "spanName", config.TracingSpec{SamplingRate: "1"})
}

func TestSpanContextFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		wantSc trace.SpanContext
		wantOk bool
	}{
		{
			name:   "future version",
			header: "02-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			wantSc: trace.SpanContext{
				TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
				SpanID:       trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
				TraceOptions: trace.TraceOptions(1),
			},
			wantOk: true,
		},
		{
			name:   "zero trace ID and span ID",
			header: "00-00000000000000000000000000000000-0000000000000000-01",
			wantSc: trace.SpanContext{},
			wantOk: false,
		},
		{
			name:   "valid header",
			header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			wantSc: trace.SpanContext{
				TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
				SpanID:       trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
				TraceOptions: trace.TraceOptions(1),
			},
			wantOk: true,
		},
		{
			name:   "missing options",
			header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
			wantSc: trace.SpanContext{},
			wantOk: false,
		},
		{
			name:   "empty options",
			header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-",
			wantSc: trace.SpanContext{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &fasthttp.Request{}
			req.Header.Add("traceparent", tt.header)

			gotSc, _ := SpanContextFromRequest(req)
			assert.Equalf(t, gotSc, tt.wantSc, "SpanContextFromRequest gotSc = %v, want %v", gotSc, tt.wantSc)
		})
	}
}

func TestSpanContextToRequest(t *testing.T) {
	tests := []struct {
		sc trace.SpanContext
	}{
		{
			sc: trace.SpanContext{
				TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
				SpanID:       trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
				TraceOptions: trace.TraceOptions(1),
			},
		},
	}
	for _, tt := range tests {
		t.Run("SpanContextToRequest", func(t *testing.T) {
			req := &fasthttp.Request{}
			SpanContextToRequest(tt.sc, req)

			got, _ := SpanContextFromRequest(req)

			assert.Equalf(t, got, tt.sc, "SpanContextToRequest() got = %v, want %v", got, tt.sc)
		})
	}
}

func getTestHTTPRequest() *fasthttp.Request {
	req := &fasthttp.Request{}
	req.Header.Set("dapr-testheaderkey", "dapr-testheadervalue")
	req.Header.Set("x-testheaderkey1", "dapr-testheadervalue")
	req.Header.Set("daprd-testheaderkey2", "dapr-testheadervalue")

	var (
		tid = trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 1, 2, 4, 8, 16, 32, 64, 128}
		sid = trace.SpanID{1, 2, 4, 8, 16, 32, 64, 128}
	)

	sc := trace.SpanContext{
		TraceID:      tid,
		SpanID:       sid,
		TraceOptions: 0x0,
	}

	SpanContextToRequest(sc, req)
	return req
}
