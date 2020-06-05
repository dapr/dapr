// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
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

func TestGetAPIComponent(t *testing.T) {
	var tests = []struct {
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
	var tests = []struct {
		path          string
		expectedType  string
		expectedValue string
	}{
		{"/v1.0/state/statestore/key", "state", "statestore"},
		{"/v1.0/state/statestore", "state", "statestore"},
		{"/v1.0/secrets/keyvault/name", "secrets", "keyvault"},
		{"/v1.0/invoke/fakeApp/method/add", "invoke", "fakeApp"},
		{"/v1/publish/topicA", "pubsub", "topicA"},
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

			reqCtx.SetUserValue("storeName", "statestore")
			reqCtx.SetUserValue("secretStoreName", "keyvault")
			reqCtx.SetUserValue("topic", "topicA")
			reqCtx.SetUserValue("name", "kafka")

			got := spanAttributesMapFromHTTPContext(reqCtx)
			_, componentType := getAPIComponent(tt.path)
			switch componentType {
			case "state":
				assert.Equal(t, got[dbTypeSpanAttributeKey], tt.expectedType)
				assert.Equal(t, got[dbInstanceSpanAttributeKey], tt.expectedValue)
				assert.Equal(t, got[dbStatementSpanAttributeKey], fmt.Sprintf("%s %s", method, tt.path))
				assert.Equal(t, got[dbURLSpanAttributeKey], tt.expectedType)

			case "secrets":
				assert.Equal(t, got[dbTypeSpanAttributeKey], tt.expectedType)
				assert.Equal(t, got[dbInstanceSpanAttributeKey], tt.expectedValue)
				assert.Equal(t, got[dbStatementSpanAttributeKey], fmt.Sprintf("%s %s", method, tt.path))
				assert.Equal(t, got[dbURLSpanAttributeKey], tt.expectedType)

			case "bindings":
				assert.Equal(t, got[dbTypeSpanAttributeKey], tt.expectedType)
				assert.Equal(t, got[dbInstanceSpanAttributeKey], tt.expectedValue)
				assert.Equal(t, got[dbStatementSpanAttributeKey], fmt.Sprintf("%s %s", method, tt.path))
				assert.Equal(t, got[dbURLSpanAttributeKey], tt.expectedType)

			case "publish":
				assert.Equal(t, got[messagingSystemSpanAttributeKey], tt.expectedType)
				assert.Equal(t, got[messagingDestinationSpanAttributeKey], tt.expectedValue)
				assert.Equal(t, got[messagingDestinationKindSpanAttributeKey], messagingDestinationTopicKind)
			}
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

	SpanContextToRequest(sc, req)
	return req
}
