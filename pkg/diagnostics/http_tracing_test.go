// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

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

func TestUserDefinedHTTPHeaders(t *testing.T) {
	reqCtx := &fasthttp.RequestCtx{}
	reqCtx.Request.Header.Add("dapr-userdefined-1", "value1")
	reqCtx.Request.Header.Add("dapr-userdefined-2", "value2")
	reqCtx.Request.Header.Add("no-attr", "value3")

	m := userDefinedHTTPHeaders(reqCtx)

	assert.Equal(t, 2, len(m))
	assert.Equal(t, "value1", m["dapr-userdefined-1"])
	assert.Equal(t, "value2", m["dapr-userdefined-2"])
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

	t.Run("empty span context", func(t *testing.T) {
		req := &fasthttp.Request{}
		sc := trace.SpanContext{}
		SpanContextToHTTPHeaders(sc, req.Header.Set)

		assert.Nil(t, req.Header.Peek(traceparentHeader))
	})
}

func TestGetAPIComponent(t *testing.T) {
	tests := []struct {
		path    string
		version string
		api     string
	}{
		{"/v1.0/state/statestore/key", "v1.0", "state"},
		{"/v1.0/state/statestore", "v1.0", "state"},
		{"/v1.0/secrets/keyvault/name", "v1.0", "secrets"},
		{"/v1.0/invoke/fakeApp/method/add", "v1.0", "invoke"},
		{"/v1/publish/topicA", "v1", "publish"},
		{"/v1/bindings/kafka", "v1", "bindings"},
		{"/healthz", "", ""},
		{"/v1/actors/DemoActor/1/state/key", "v1", "actors"},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ver, api := getAPIComponent(tt.path)
			assert.Equal(t, tt.version, ver)
			assert.Equal(t, tt.api, api)
		})
	}
}

func TestGetSpanAttributesMapFromHTTPContext(t *testing.T) {
	tests := []struct {
		path string
		out  map[string]string
	}{
		{
			"/v1.0/state/statestore/key",
			map[string]string{
				dbSystemSpanAttributeKey:           "state",
				dbNameSpanAttributeKey:             "statestore",
				dbStatementSpanAttributeKey:        "GET /v1.0/state/statestore/key",
				dbConnectionStringSpanAttributeKey: "state",
			},
		},
		{
			"/v1.0/state/statestore",
			map[string]string{
				dbSystemSpanAttributeKey:           "state",
				dbNameSpanAttributeKey:             "statestore",
				dbStatementSpanAttributeKey:        "GET /v1.0/state/statestore",
				dbConnectionStringSpanAttributeKey: "state",
			},
		},
		{
			"/v1.0/secrets/keyvault/name",
			map[string]string{
				dbSystemSpanAttributeKey:           secretBuildingBlockType,
				dbNameSpanAttributeKey:             "keyvault",
				dbStatementSpanAttributeKey:        "GET /v1.0/secrets/keyvault/name",
				dbConnectionStringSpanAttributeKey: secretBuildingBlockType,
			},
		},
		{
			"/v1.0/invoke/fakeApp/method/add",
			map[string]string{
				gRPCServiceSpanAttributeKey: daprGRPCServiceInvocationService,
				netPeerNameSpanAttributeKey: "fakeApp",
				daprAPISpanNameInternal:     "CallLocal/fakeApp/add",
			},
		},
		{
			"/v1/publish/topicA",
			map[string]string{
				messagingSystemSpanAttributeKey:          pubsubBuildingBlockType,
				messagingDestinationSpanAttributeKey:     "topicA",
				messagingDestinationKindSpanAttributeKey: messagingDestinationTopicKind,
			},
		},
		{
			"/v1/bindings/kafka",
			map[string]string{
				dbSystemSpanAttributeKey:           bindingBuildingBlockType,
				dbNameSpanAttributeKey:             "kafka",
				dbStatementSpanAttributeKey:        "GET /v1/bindings/kafka",
				dbConnectionStringSpanAttributeKey: bindingBuildingBlockType,
			},
		},
		{
			"/v1.0/actors/demo_actor/1/state/my_data",
			map[string]string{
				dbSystemSpanAttributeKey:           stateBuildingBlockType,
				dbNameSpanAttributeKey:             "actor",
				dbStatementSpanAttributeKey:        "GET /v1.0/actors/demo_actor/1/state/my_data",
				dbConnectionStringSpanAttributeKey: stateBuildingBlockType,
				daprAPIActorTypeID:                 "demo_actor.1",
			},
		},
		{
			"/v1.0/actors/demo_actor/1/method/method1",
			map[string]string{
				gRPCServiceSpanAttributeKey: daprGRPCServiceInvocationService,
				netPeerNameSpanAttributeKey: "demo_actor.1",
				daprAPIActorTypeID:          "demo_actor.1",
				daprAPISpanNameInternal:     "CallActor/demo_actor/add",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := getTestHTTPRequest()
			resp := &fasthttp.Response{}
			resp.SetStatusCode(200)
			req.SetRequestURI(tt.path)
			reqCtx := &fasthttp.RequestCtx{}
			req.CopyTo(&reqCtx.Request)

			reqCtx.SetUserValue("storeName", "statestore")
			reqCtx.SetUserValue("secretStoreName", "keyvault")
			reqCtx.SetUserValue("topic", "topicA")
			reqCtx.SetUserValue("name", "kafka")
			reqCtx.SetUserValue("id", "fakeApp")
			reqCtx.SetUserValue("method", "add")
			reqCtx.SetUserValue("actorType", "demo_actor")
			reqCtx.SetUserValue("actorId", "1")

			got := spanAttributesMapFromHTTPContext(reqCtx)
			for k, v := range tt.out {
				assert.Equal(t, v, got[k])
			}
		})
	}
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
			got, _ := SpanContextFromW3CString(h)

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

func TestHTTPTraceMiddleware(t *testing.T) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	fakeHandler := func(ctx *fasthttp.RequestCtx) {
		time.Sleep(100 * time.Millisecond)
		ctx.Response.SetBodyRaw([]byte(responseBody))
	}

	rate := config.TracingSpec{SamplingRate: "1"}
	handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", rate)

	t.Run("traceparent is given in request and sampling is enabled", func(t *testing.T) {
		testRequestCtx := newTraceFastHTTPRequestCtx(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
			map[string]string{},
		)
		handler(testRequestCtx)
		span := diag_utils.SpanFromContext(testRequestCtx)
		sc := span.SpanContext()
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", fmt.Sprintf("%x", sc.TraceID[:]))
		assert.NotEqual(t, "00f067aa0ba902b7", fmt.Sprintf("%x", sc.SpanID[:]))
	})

	t.Run("traceparent is not given in request", func(t *testing.T) {
		testRequestCtx := newTraceFastHTTPRequestCtx(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"dapr-userdefined": "value",
			},
			map[string]string{},
		)
		handler(testRequestCtx)
		span := diag_utils.SpanFromContext(testRequestCtx)
		sc := span.SpanContext()
		assert.NotEmpty(t, fmt.Sprintf("%x", sc.TraceID[:]))
		assert.NotEmpty(t, fmt.Sprintf("%x", sc.SpanID[:]))
	})

	t.Run("traceparent not given in response", func(t *testing.T) {
		testRequestCtx := newTraceFastHTTPRequestCtx(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"dapr-userdefined": "value",
			},
			map[string]string{},
		)
		handler(testRequestCtx)
		span := diag_utils.SpanFromContext(testRequestCtx)
		sc := span.SpanContext()
		assert.Equal(t, testRequestCtx.Response.Header.Peek(traceparentHeader), []byte(SpanContextToW3CString(sc)))
	})

	t.Run("traceparent given in response", func(t *testing.T) {
		testRequestCtx := newTraceFastHTTPRequestCtx(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"dapr-userdefined": "value",
			},
			map[string]string{
				traceparentHeader: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
				tracestateHeader:  "xyz=t61pCWkhMzZ",
			},
		)
		handler(testRequestCtx)
		span := diag_utils.SpanFromContext(testRequestCtx)
		sc := span.SpanContext()
		assert.NotEqual(t, testRequestCtx.Response.Header.Peek(traceparentHeader), []byte(SpanContextToW3CString(sc)))
	})

	t.Run("path is /v1.0/invoke/*", func(t *testing.T) {
		testRequestCtx := newTraceFastHTTPRequestCtx(
			requestBody, "/v1.0/invoke/callee/method/method1",
			map[string]string{},
			map[string]string{},
		)
		testRequestCtx.SetUserValue("id", "callee")
		testRequestCtx.SetUserValue("method", "method1")

		// act
		handler(testRequestCtx)

		// assert
		span := diag_utils.SpanFromContext(testRequestCtx)
		sc := span.SpanContext()
		assert.True(t, strings.Contains(span.String(), "CallLocal/callee/method1"))
		assert.NotEmpty(t, fmt.Sprintf("%x", sc.TraceID[:]))
		assert.NotEmpty(t, fmt.Sprintf("%x", sc.SpanID[:]))
	})
}

func newTraceFastHTTPRequestCtx(expectedBody, expectedRequestURI string, expectedRequestHeader map[string]string, expectedResponseHeader map[string]string) *fasthttp.RequestCtx {
	expectedMethod := fasthttp.MethodPost
	expectedTransferEncoding := "encoding"
	expectedHost := "dapr.io"
	expectedRemoteAddr := "1.2.3.4:6789"

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request

	req.Header.SetMethod(expectedMethod)
	req.SetRequestURI(expectedRequestURI)
	req.Header.SetHost(expectedHost)
	req.Header.Add(fasthttp.HeaderTransferEncoding, expectedTransferEncoding)
	req.Header.SetContentLength(len([]byte(expectedBody)))
	req.BodyWriter().Write([]byte(expectedBody)) // nolint:errcheck

	for k, v := range expectedRequestHeader {
		req.Header.Set(k, v)
	}

	remoteAddr, _ := net.ResolveTCPAddr("tcp", expectedRemoteAddr)

	ctx.Init(&req, remoteAddr, nil)

	for k, v := range expectedResponseHeader {
		ctx.Response.Header.Set(k, v)
	}

	return &ctx
}
