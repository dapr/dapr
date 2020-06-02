// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"fmt"
	"net/textproto"
	"testing"

	"github.com/dapr/dapr/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
)

func TestStartClientSpanTracing(t *testing.T) {
	req := getTestHTTPRequest()
	reqCtx := &fasthttp.RequestCtx{}
	req.CopyTo(&reqCtx.Request)

	StartTracingClientSpanFromHTTPContext(context.Background(), "test", config.TracingSpec{SamplingRate: "0.5"})
}

func TestTracingClientSpanFromHTTPContext(t *testing.T) {
	req := getTestHTTPRequest()
	reqCtx := &fasthttp.RequestCtx{}
	req.CopyTo(&reqCtx.Request)
	spec := config.TracingSpec{SamplingRate: "1"}
	sc := GetSpanContextFromRequestContext(reqCtx, spec)
	ctx := NewContext((context.Context)(reqCtx), sc)
	StartTracingClientSpanFromHTTPContext(ctx, "spanName", config.TracingSpec{SamplingRate: "1"})
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

func TestSpanContextToHTTPHeaders(t *testing.T) {
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
		t.Run("SpanContextToHTTPHeaders", func(t *testing.T) {
			req := &fasthttp.Request{}
			SpanContextToHTTPHeaders(tt.sc, req.Header.Set)

			got, _ := SpanContextFromRequest(req)

			assert.Equalf(t, got, tt.sc, "SpanContextToHTTPHeaders() got = %v, want %v", got, tt.sc)
		})
	}
}

func TestWithNoSpanContext(t *testing.T) {
	t.Run("No SpanContext with always sampling rate", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		spec := config.TracingSpec{SamplingRate: "1"}
		sc := GetSpanContextFromRequestContext(ctx, spec)
		assert.NotEmpty(t, sc, "Should get default span context")
		assert.NotEmpty(t, sc.TraceID, "Should get default traceID")
		assert.NotEmpty(t, sc.SpanID, "Should get default spanID")
		assert.Equal(t, 1, int(sc.TraceOptions), "Should be sampled")
	})

	t.Run("No SpanContext with non-zero sampling rate", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		spec := config.TracingSpec{SamplingRate: "0.5"}
		sc := GetSpanContextFromRequestContext(ctx, spec)
		assert.NotEmpty(t, sc, "Should get default span context")
		assert.NotEmpty(t, sc.TraceID, "Should get default traceID")
		assert.NotEmpty(t, sc.SpanID, "Should get default spanID")
	})

	t.Run("No SpanContext with zero sampling rate", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Request: fasthttp.Request{}}
		spec := config.TracingSpec{SamplingRate: "0"}
		sc := GetSpanContextFromRequestContext(ctx, spec)
		assert.NotEmpty(t, sc, "Should get default span context")
		assert.NotEmpty(t, sc.TraceID, "Should get default traceID")
		assert.NotEmpty(t, sc.SpanID, "Should get default spanID")
		assert.Equal(t, 0, int(sc.TraceOptions), "Should not be sampled")
	})
}

func TestGetAPIComponent(t *testing.T) {
	state := apiComponent{componentType: "state", componentValue: "statestore"}
	secret := apiComponent{componentType: "secrets", componentValue: "keyvault"}
	invoke := apiComponent{componentType: "invoke", componentValue: "fakeApp"}
	publish := apiComponent{componentType: "publish", componentValue: "topicA"}
	bindings := apiComponent{componentType: "bindings", componentValue: "kafka"}
	empty := apiComponent{}
	actors := apiComponent{componentType: "actors", componentValue: "DemoActor"}

	var tests = []struct {
		path string
		want apiComponent
	}{
		{"/v1.0/state/statestore/key", state},
		{"/v1.0/state/statestore", state},
		{"/v1.0/secrets/keyvault/name", secret},
		{"/v1.0/invoke/fakeApp/method/add", invoke},
		{"/v1/publish/topicA", publish},
		{"/v1/bindings/kafka", bindings},
		{"/healthz", empty},
		{"/v1/actors/DemoActor/1/state/key", actors},
		{"", empty},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := getAPIComponent(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetSpanAttributesMapFromHTTPContext(t *testing.T) {
	var tests = []struct {
		path          string
		expectedType  string
		expectedValue string
	}{
		{"/v1.0/state/statestore/key", "state", "statestore"},
		{"/v1.0/state/statestore", "state", "statestore"},
		{"/v1.0/secrets/keyvault/name", "secrets", "keyvault"},
		{"/v1.0/invoke/fakeApp/method/add", "invoke", "fakeApp"},
		{"/v1/publish/topicA", "publish", "topicA"},
		{"/v1/bindings/kafka", "bindings", "kafka"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := getTestHTTPRequest()
			resp := &fasthttp.Response{}
			resp.SetStatusCode(200)
			req.SetRequestURI(tt.path)
			reqCtx := &fasthttp.RequestCtx{}
			req.CopyTo(&reqCtx.Request)
			method := string(req.Header.Method())

			want := map[string]string{}
			switch tt.expectedType {
			case "state", "secrets", "bindings":
				want[dbTypeSpanAttributeKey] = tt.expectedType
				want[dbInstanceSpanAttributeKey] = tt.expectedValue
				want[dbStatementSpanAttributeKey] = fmt.Sprintf("%s %s", method, tt.path)
				want[dbURLSpanAttributeKey] = tt.path
			case "invoke", "actors":
				want[httpMethodSpanAttributeKey] = method
				want[httpURLSpanAttributeKey] = reqCtx.Request.URI().String()
				want[httpStatusCodeSpanAttributeKey] = "200"
				want[httpStatusTextSpanAttributeKey] = "OK"
			case "publish":
				want[messagingSystemSpanAttributeKey] = tt.expectedType
				want[messagingDestinationSpanAttributeKey] = tt.expectedValue
				want[messagingDestinationKindSpanAttributeKey] = messagingDestinationKind
			}

			got := getSpanAttributesMapFromHTTPContext(reqCtx)
			assert.Equal(t, want, got)
		})
	}
}

func TestUpdateResponseTraceHeadersHTTP(t *testing.T) {
	daprSC := trace.SpanContext{
		TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
		SpanID:       trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
		TraceOptions: trace.TraceOptions(1),
	}
	t.Run("No SpanContext found in the response headers", func(t *testing.T) {
		ctx := &fasthttp.RequestCtx{Response: fasthttp.Response{}}
		UpdateResponseTraceHeadersHTTP(ctx, daprSC)
		s := string(ctx.Response.Header.Peek(textproto.CanonicalMIMEHeaderKey("traceparent")))
		got, _ := SpanContextFromString(s)
		assert.NotEmpty(t, s, "Should get span context")
		assert.Equal(t, daprSC.TraceID, got.TraceID, "Should get generated traceID")
		assert.Equal(t, daprSC.SpanID, got.SpanID, "Should get generated spanID")
		assert.Equal(t, daprSC.TraceOptions, got.TraceOptions, "Should get generated traceOptions")
	})

	t.Run("SpanContext found in the response headers", func(t *testing.T) {
		sc := trace.SpanContext{
			TraceID:      trace.TraceID{35, 149, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
			SpanID:       trace.SpanID{0, 220, 103, 170, 10, 169, 2, 183},
			TraceOptions: trace.TraceOptions(1),
		}
		ctx := &fasthttp.RequestCtx{Response: fasthttp.Response{}}
		SpanContextToHTTPHeaders(sc, ctx.Response.Header.Set)

		// test
		UpdateResponseTraceHeadersHTTP(ctx, daprSC)
		s := string(ctx.Response.Header.Peek(textproto.CanonicalMIMEHeaderKey("traceparent")))
		got, _ := SpanContextFromString(s)
		assert.NotEmpty(t, s, "Should get span context")
		assert.NotEqual(t, daprSC.TraceID, got.TraceID, "Dapr generated and client generated traceID should be different")
		assert.NotEqual(t, daprSC.SpanID, got.SpanID, "Dapr generated and client generated spanID should be different")

		assert.Equal(t, sc.TraceID, got.TraceID, "Should get client generated traceID")
		assert.Equal(t, sc.SpanID, got.SpanID, "Should get client generated spanID")
		assert.Equal(t, sc.TraceOptions, got.TraceOptions, "Should get client generated traceOptions")
	})
}

func TestSpanContextToResponse(t *testing.T) {
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
		t.Run("SpanContextToResponse", func(t *testing.T) {
			resp := &fasthttp.Response{}
			SpanContextToHTTPHeaders(tt.sc, resp.Header.Set)

			h := string(resp.Header.Peek(textproto.CanonicalMIMEHeaderKey("traceparent")))
			got, _ := SpanContextFromString(h)

			assert.Equalf(t, got, tt.sc, "SpanContextToResponse() got = %v, want %v", got, tt.sc)
		})
	}
}

func getTestHTTPRequest() *fasthttp.Request {
	req := &fasthttp.Request{}
	req.SetRequestURI("/v1.0/state/statestore/key")
	req.Header.Set("dapr-testheaderkey", "dapr-testheadervalue")
	req.Header.Set("x-testheaderkey1", "dapr-testheadervalue")
	req.Header.Set("daprd-testheaderkey2", "dapr-testheadervalue")
	req.Header.SetMethod(fasthttp.MethodGet)

	var (
		tid = trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 1, 2, 4, 8, 16, 32, 64, 128}
		sid = trace.SpanID{1, 2, 4, 8, 16, 32, 64, 128}
	)

	sc := trace.SpanContext{
		TraceID:      tid,
		SpanID:       sid,
		TraceOptions: 0x0,
	}

	SpanContextToHTTPHeaders(sc, req.Header.Set)
	return req
}
