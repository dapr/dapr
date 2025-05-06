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
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel"
	otelbaggage "go.opentelemetry.io/otel/baggage"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/config"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/responsewriter"
)

func TestSpanContextFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		wantSc trace.SpanContextConfig
		wantOk bool
	}{
		{
			name:   "future version",
			header: "02-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			wantSc: trace.SpanContextConfig{
				TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
				SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
				TraceFlags: trace.TraceFlags(1),
			},
			wantOk: true,
		},
		{
			name:   "zero trace ID and span ID",
			header: "00-00000000000000000000000000000000-0000000000000000-01",
			wantSc: trace.SpanContextConfig{},
			wantOk: false,
		},
		{
			name:   "valid header",
			header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			wantSc: trace.SpanContextConfig{
				TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
				SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
				TraceFlags: trace.TraceFlags(1),
			},
			wantOk: true,
		},
		{
			name:   "missing options",
			header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
			wantSc: trace.SpanContextConfig{},
			wantOk: false,
		},
		{
			name:   "empty options",
			header: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-",
			wantSc: trace.SpanContextConfig{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				Header: make(http.Header),
			}
			req.Header.Add("traceparent", tt.header)

			gotSc := SpanContextFromRequest(req)
			wantSc := trace.NewSpanContext(tt.wantSc)
			assert.Equalf(t, wantSc, gotSc, "SpanContextFromRequest gotSc = %v, want %v", gotSc, wantSc)
		})
	}
}

func TestUserDefinedHTTPHeaders(t *testing.T) {
	req := &http.Request{
		Header: make(http.Header),
	}
	req.Header.Add("dapr-userdefined-1", "value1")
	req.Header.Add("dapr-userdefined-2", "value2")
	req.Header.Add("no-attr", "value3")

	m := userDefinedHTTPHeaders(req)

	assert.Len(t, m, 2)
	assert.Equal(t, "value1", m["dapr-userdefined-1"])
	assert.Equal(t, "value2", m["dapr-userdefined-2"])
}

func TestSpanContextToHTTPHeaders(t *testing.T) {
	tests := []struct {
		sc trace.SpanContextConfig
	}{
		{
			sc: trace.SpanContextConfig{
				TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
				SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
				TraceFlags: trace.TraceFlags(1),
			},
		},
	}
	for _, tt := range tests {
		t.Run("SpanContextToHTTPHeaders", func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "http://test.local/path", nil)
			wantSc := trace.NewSpanContext(tt.sc)
			SpanContextToHTTPHeaders(wantSc, req.Header.Set)

			got := SpanContextFromRequest(req)

			assert.Equalf(t, wantSc, got, "SpanContextToHTTPHeaders() got = %v, want %v", got, wantSc)
		})
	}

	t.Run("empty span context", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://test.local/path", nil)
		sc := trace.SpanContext{}
		SpanContextToHTTPHeaders(sc, req.Header.Set)

		assert.Empty(t, req.Header.Get("traceparent"))
	})
}

func TestGetSpanAttributesMapFromHTTPContext(t *testing.T) {
	tests := []struct {
		path               string
		appendAttributesFn endpoints.AppendSpanAttributesFn
		out                map[string]string
	}{
		{
			"/v1.0/state/statestore/key",
			func(r *http.Request, m map[string]string) {
				m[diagConsts.DBSystemSpanAttributeKey] = "state"
				m[diagConsts.DBNameSpanAttributeKey] = "statestore"
				m[diagConsts.DBConnectionStringSpanAttributeKey] = "state"
			},
			map[string]string{
				diagConsts.DaprAPIProtocolSpanAttributeKey:    "http",
				diagConsts.DaprAPISpanAttributeKey:            "GET /v1.0/state/statestore/key",
				diagConsts.DBSystemSpanAttributeKey:           "state",
				diagConsts.DBNameSpanAttributeKey:             "statestore",
				diagConsts.DBConnectionStringSpanAttributeKey: "state",
			},
		},
	}

	for i, tt := range tests {
		t.Run(fmt.Sprintf("test %d", i), func(t *testing.T) {
			var err error
			req := getTestHTTPRequest()
			resp := responsewriter.EnsureResponseWriter(httptest.NewRecorder())
			resp.WriteHeader(http.StatusOK)
			req.URL, err = url.Parse("http://test.local" + tt.path)
			require.NoError(t, err)

			ctx := context.WithValue(req.Context(), endpoints.EndpointCtxKey{}, &endpoints.EndpointCtxData{
				Group: &endpoints.EndpointGroup{
					AppendSpanAttributes: tt.appendAttributesFn,
				},
			})
			req = req.WithContext(ctx)

			got := spanAttributesMapFromHTTPContext(responsewriter.EnsureResponseWriter(resp), req)
			for k, v := range tt.out {
				assert.Equalf(t, v, got[k], "key: %v", k)
			}
		})
	}
}

func TestSpanContextToResponse(t *testing.T) {
	tests := []struct {
		scConfig trace.SpanContextConfig
	}{
		{
			scConfig: trace.SpanContextConfig{
				TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
				SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
				TraceFlags: trace.TraceFlags(1),
			},
		},
	}
	for _, tt := range tests {
		t.Run("SpanContextToResponse", func(t *testing.T) {
			resp := httptest.NewRecorder()
			wantSc := trace.NewSpanContext(tt.scConfig)
			SpanContextToHTTPHeaders(wantSc, resp.Header().Set)

			h := resp.Header().Get("traceparent")
			got, _ := SpanContextFromW3CString(h)

			assert.Equalf(t, wantSc, got, "SpanContextToResponse() got = %v, want %v", got, wantSc)
		})
	}
}

func getTestHTTPRequest() *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "http://test.local/v1.0/state/statestore/key", nil)
	req.Header.Set("dapr-testheaderkey", "dapr-testheadervalue")
	req.Header.Set("x-testheaderkey1", "dapr-testheadervalue")
	req.Header.Set("daprd-testheaderkey2", "dapr-testheadervalue")

	var (
		tid = trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 1, 2, 4, 8, 16, 32, 64, 128}
		sid = trace.SpanID{1, 2, 4, 8, 16, 32, 64, 128}
	)

	scConfig := trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: 0x0,
	}
	sc := trace.NewSpanContext(scConfig)
	SpanContextToHTTPHeaders(sc, req.Header.Set)
	return req
}

func TestHTTPTraceMiddleware(t *testing.T) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	fakeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte(responseBody))
	})

	rate := config.TracingSpec{SamplingRate: "1"}
	handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", rate)

	exp := newOtelFakeExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer func() { _ = tp.Shutdown(t.Context()) }()
	otel.SetTracerProvider(tp)

	t.Run("traceparent is given in request and sampling is enabled", func(t *testing.T) {
		r := newTraceRequest(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			},
		)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		span := diagUtils.SpanFromContext(r.Context())
		sc := span.SpanContext()
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", hex.EncodeToString(traceID[:]))
		assert.NotEqual(t, "00f067aa0ba902b7", hex.EncodeToString(spanID[:]))
	})

	t.Run("traceparent is not given in request", func(t *testing.T) {
		r := newTraceRequest(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"dapr-userdefined": "value",
			},
		)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		span := diagUtils.SpanFromContext(r.Context())
		sc := span.SpanContext()
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.NotEmpty(t, hex.EncodeToString(traceID[:]))
		assert.NotEmpty(t, hex.EncodeToString(spanID[:]))
	})

	t.Run("traceparent not given in response", func(t *testing.T) {
		r := newTraceRequest(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"dapr-userdefined": "value",
			},
		)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		span := diagUtils.SpanFromContext(r.Context())
		sc := span.SpanContext()
		assert.Equal(t, w.Header().Get("traceparent"), SpanContextToW3CString(sc))
	})

	t.Run("traceparent given in response", func(t *testing.T) {
		r := newTraceRequest(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"dapr-userdefined": "value",
			},
		)
		w := httptest.NewRecorder()
		w.Header().Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		w.Header().Set("tracestate", "xyz=t61pCWkhMzZ")
		handler.ServeHTTP(w, r)
		span := diagUtils.SpanFromContext(r.Context())
		sc := span.SpanContext()
		assert.NotEqual(t, w.Header().Get("traceparent"), SpanContextToW3CString(sc))
	})

	t.Run("baggage from context only", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1;prop1=propvalue1")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify baggage is in ctx
		baggage := otelbaggage.FromContext(req.Context())
		assert.NotNil(t, baggage)
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())
		propVal, exists := member.Properties()[0].Value()
		assert.True(t, exists)
		assert.Equal(t, "propvalue1", propVal)

		// Verify baggage is NOT in headers
		assert.Empty(t, rr.Header().Get("baggage"))
	})

	t.Run("baggage from metadata only", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("baggage", "key1=value1;prop1=propvalue1")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify baggage is NOT in ctx
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members())

		// Verify baggage is in headers
		assert.Equal(t, "key1=value1;prop1=propvalue1", rr.Header().Get("baggage"))
	})

	t.Run("baggage from both context and header", func(t *testing.T) {
		bag, err := otelbaggage.Parse("ctx1=ctxval1;prop1=propvalue1")
		require.NoError(t, err)

		// Create request with both context and header baggage
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)
		req.Header.Set("baggage", "meta1=metaval1;prop1=metaprop1")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		baggage := otelbaggage.FromContext(req.Context())
		assert.NotNil(t, baggage)
		member := baggage.Member("ctx1")
		assert.Equal(t, "ctxval1", member.Value())
		propVal, exists := member.Properties()[0].Value()
		assert.True(t, exists)
		assert.Equal(t, "propvalue1", propVal)

		assert.Equal(t, "meta1=metaval1;prop1=metaprop1", rr.Header().Get("baggage"))
	})

	t.Run("empty header baggage propagates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify empty baggage in header
		assert.Equal(t, "", rr.Header().Get("baggage"))

		// Verify no baggage in context
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members(), "baggage should be empty")
	})

	t.Run("empty context baggage propagates", func(t *testing.T) {
		bag, err := otelbaggage.New()
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify no baggage in header
		assert.Empty(t, rr.Header().Get("baggage"))

		// Verify empty baggage in context
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members(), "baggage should be empty")
	})

	t.Run("header baggage with properties", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, "key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2", rr.Header().Get("baggage"))

		// Verify baggage is NOT in ctx
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members())
	})

	t.Run("context baggage with properties", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		bag, err := otelbaggage.Parse("key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2")
		require.NoError(t, err)

		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		assert.Empty(t, rr.Header().Get("baggage"))

		// Verify baggage is in ctx w/ both members & properties
		baggage := otelbaggage.FromContext(req.Context())
		assert.NotNil(t, baggage, "baggage should be in ctx")
		assert.Len(t, baggage.Members(), 2, "should have 2 baggage members")

		// key1
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())
		props := member.Properties()
		assert.Len(t, props, 1, "should have one property")
		assert.Equal(t, "prop1", props[0].Key())
		propValue, exists := props[0].Value()
		assert.True(t, exists, "property should exist")
		assert.Equal(t, "propvalue1", propValue)
		// key2
		member = baggage.Member("key2")
		assert.Equal(t, "value2", member.Value())
		props = member.Properties()
		assert.Len(t, props, 1, "should have one property")
		assert.Equal(t, "prop2", props[0].Key())
		propValue, exists = props[0].Value()
		assert.True(t, exists, "property should exist")
		assert.Equal(t, "propvalue2", propValue)
	})

	t.Run("header baggage with special characters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "key1=value1%20with%20spaces,key2=value2%2Fwith%2Fslashes")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify baggage is preserved in response headers with encoded values
		assert.Equal(t, "key1=value1%20with%20spaces,key2=value2%2Fwith%2Fslashes", rr.Header().Get("baggage"))

		// Verify baggage is NOT in ctx
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members())
	})

	t.Run("context baggage with special characters", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value2%2Fwith%2Fslashes;prop1=value1%20with%20spaces")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify baggage is in ctx with special chars (URL-decoded by OpenTelemetry)
		baggage := otelbaggage.FromContext(req.Context())
		assert.NotNil(t, baggage)
		member := baggage.Member("key1")
		assert.Equal(t, "value2/with/slashes", member.Value())
		propVal, exists := member.Properties()[0].Value()
		assert.True(t, exists)
		assert.Equal(t, "value1 with spaces", propVal)

		// Verify baggage is NOT in headers
		assert.Empty(t, rr.Header().Get("baggage"))
	})

	t.Run("invalid baggage header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "invalid-baggage")

		rr := httptest.NewRecorder()
		fakeHandler1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
		handler := HTTPTraceMiddleware(fakeHandler1, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "invalid baggage header")

		// Verify invalid baggage is NOT propagated in headers
		assert.Empty(t, rr.Header().Get("baggage"))
	})

	t.Run("multiple baggage values in header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "key1=value1,key2=value2")

		rr := httptest.NewRecorder()
		fakeHandler2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
		handler := HTTPTraceMiddleware(fakeHandler2, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, "key1=value1,key2=value2", rr.Header().Get("baggage"))
	})

	t.Run("multiple baggage values in ctx", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify baggage is NOT in headers
		assert.Empty(t, rr.Header().Get("baggage"))

		// Verify both members in ctx
		baggage := otelbaggage.FromContext(req.Context())
		assert.NotNil(t, baggage)
		assert.Len(t, baggage.Members(), 2)

		// member1
		member := baggage.Member("key1")
		assert.Equal(t, "value1", member.Value())
		propVal, exists := member.Properties()[0].Value()
		assert.True(t, exists)
		assert.Equal(t, "propvalue1", propVal)
		// member2
		member = baggage.Member("key2")
		assert.Equal(t, "value2", member.Value())
		propVal, exists = member.Properties()[0].Value()
		assert.True(t, exists)
		assert.Equal(t, "propvalue2", propVal)
	})

	t.Run("empty metadata baggage propagates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("baggage", "")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		// Verify baggage is empty in ctx
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members())

		// Verify no baggage in headers
		assert.Empty(t, rr.Header().Get("baggage"))
	})

	t.Run("mixed valid and invalid header baggage items", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "key1=value1,invalid-format-no-equals,key2=value2")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "invalid baggage header")

		assert.Empty(t, rr.Header().Get("baggage"))
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members(), "baggage should be empty since it was rejected")
	})

	t.Run("baggage with same key in context and metadata", func(t *testing.T) {
		bag, err := otelbaggage.Parse("key1=ctxval;prop1=ctxprop")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)
		req.Header.Set("baggage", "key1=headerval;prop1=headerprop")

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		baggage := otelbaggage.FromContext(req.Context())
		assert.NotNil(t, baggage)
		member := baggage.Member("key1")
		assert.Equal(t, "ctxval", member.Value())
		propVal, exists := member.Properties()[0].Value()
		assert.True(t, exists)
		assert.Equal(t, "ctxprop", propVal)

		assert.Equal(t, "key1=headerval;prop1=headerprop", rr.Header().Get("baggage"))
	})

	t.Run("header baggage at max item length", func(t *testing.T) {
		// Create req with baggage header at exactly max per-member len
		// "key1=value1,key2=" is 17 bytes, so we need MaxBaggageBytesPerMember-17 bytes of 'x's
		existingBaggageByteCount := len("key1=value1,key2=")
		maxBaggageBytesPerMember := 4096 // OpenTelemetry limit: https://github.com/open-telemetry/opentelemetry-go/blob/main/baggage/baggage.go
		longValue := strings.Repeat("x", maxBaggageBytesPerMember-existingBaggageByteCount)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "key1=value1,key2="+longValue)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, "key1=value1,key2="+longValue, rr.Header().Get("baggage"))

		// Verify baggage is NOT in context
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members())
	})

	t.Run("context baggage at max item length", func(t *testing.T) {
		// Create req with ctx baggage at exactly max per-member len
		// "key1=value1,key2=" is 17 bytes, so we need MaxBaggageBytesPerMember-17 bytes of 'x's
		existingBaggageByteCount := len("key1=value1,key2=")
		maxBaggageBytesPerMember := 4096 // OpenTelemetry limit
		longValue := strings.Repeat("x", maxBaggageBytesPerMember-existingBaggageByteCount)
		// Create baggage with max length value
		bag, err := otelbaggage.Parse("key1=value1,key2=" + longValue)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := otelbaggage.ContextWithBaggage(req.Context(), bag)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		baggage := otelbaggage.FromContext(req.Context())
		assert.NotNil(t, baggage)
		assert.Len(t, baggage.Members(), 2)
		member := baggage.Member("key2")
		assert.Equal(t, longValue, member.Value())

		assert.Empty(t, rr.Header().Get("baggage"))
	})

	t.Run("header baggage exceeding max item length", func(t *testing.T) {
		// MaxBaggageLength is the maximum length of a baggage header according to W3C spec
		// Reference: https://www.w3.org/TR/baggage/#limits
		maxBagLen := 8192
		longValue := strings.Repeat("x", maxBagLen)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("baggage", "key1=value1,key2="+longValue)

		rr := httptest.NewRecorder()
		handler := HTTPTraceMiddleware(fakeHandler, "fakeAppID", config.TracingSpec{SamplingRate: "1"})
		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
		assert.Contains(t, rr.Body.String(), "invalid baggage header")

		// OpenTelemetry rejects entire baggage if any item exceeds length limit
		assert.Empty(t, rr.Header().Get("baggage"))

		// Verify baggage is in ctx, but empty (OpenTelemetry creates empty baggage)
		baggage := otelbaggage.FromContext(req.Context())
		assert.Empty(t, baggage.Members(), "baggage should be empty since it was rejected")
	})
}

func TestTraceStatusFromHTTPCode(t *testing.T) {
	tests := []struct {
		httpCode                int
		wantOtelCode            otelcodes.Code
		wantOtelCodeDescription string
	}{
		{
			httpCode:                200,
			wantOtelCode:            otelcodes.Unset,
			wantOtelCodeDescription: "",
		},
		{
			httpCode:                401,
			wantOtelCode:            otelcodes.Error,
			wantOtelCodeDescription: "Code(401): Unauthorized",
		},
		{
			httpCode:                488,
			wantOtelCode:            otelcodes.Error,
			wantOtelCodeDescription: "Code(488): Unknown",
		},
	}
	for _, tt := range tests {
		t.Run("traceStatusFromHTTPCode", func(t *testing.T) {
			gotOtelCode, gotOtelCodeDescription := traceStatusFromHTTPCode(tt.httpCode)
			assert.Equalf(t, tt.wantOtelCode, gotOtelCode, "traceStatusFromHTTPCode(%v) got = %v, want %v", tt.httpCode, gotOtelCode, tt.wantOtelCode)
			assert.Equalf(t, tt.wantOtelCodeDescription, gotOtelCodeDescription, "traceStatusFromHTTPCode(%v) got = %v, want %v", tt.httpCode, gotOtelCodeDescription, tt.wantOtelCodeDescription)
		})
	}
}

func newTraceRequest(body, requestPath string, requestHeader map[string]string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "http://dapr.io"+requestPath, strings.NewReader(body))
	req.Header.Set("Transfer-Encoding", "encoding")
	req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	for k, v := range requestHeader {
		req.Header.Set(k, v)
	}
	return req
}
