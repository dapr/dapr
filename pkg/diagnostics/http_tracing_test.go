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
			req.Header.Add(diagConsts.TraceparentHeader, tt.header)

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

		assert.Empty(t, req.Header.Get(diagConsts.TraceparentHeader))
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

			h := resp.Header().Get(diagConsts.TraceparentHeader)
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
				diagConsts.TraceparentHeader: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
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
		assert.Equal(t, w.Header().Get(diagConsts.TraceparentHeader), SpanContextToW3CString(sc))
	})

	t.Run("traceparent given in response", func(t *testing.T) {
		r := newTraceRequest(
			requestBody, "/v1.0/state/statestore",
			map[string]string{
				"dapr-userdefined": "value",
			},
		)
		w := httptest.NewRecorder()
		w.Header().Set(diagConsts.TraceparentHeader, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		w.Header().Set(diagConsts.TracestateHeader, "xyz=t61pCWkhMzZ")
		handler.ServeHTTP(w, r)
		span := diagUtils.SpanFromContext(r.Context())
		sc := span.SpanContext()
		assert.NotEqual(t, w.Header().Get(diagConsts.TraceparentHeader), SpanContextToW3CString(sc))
	})

	t.Run("baggage header propagation", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, "key1=value1", rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("empty baggage header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Empty(t, rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("baggage with properties", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, "key1=value1;prop1=propvalue1,key2=value2;prop2=propvalue2", rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("baggage with special characters", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1%20with%20spaces,key2=value2%2Fwith%2Fslashes")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, "key1=value1%20with%20spaces,key2=value2%2Fwith%2Fslashes", rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("invalid baggage header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "invalid-baggage")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Empty(t, rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("multiple baggage values in header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1,key2=value2")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		assert.Equal(t, "key1=value1,key2=value2", rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("mixed valid and invalid baggage items", func(t *testing.T) {
		// Create request with both valid and invalid baggage
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1,invalid-format-no-equals,key2=value2")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		// Only valid baggage items should be propagated
		assert.Equal(t, "key1=value1,key2=value2", rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("baggage with max length", func(t *testing.T) {
		// Create test request with baggage at max length
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1,key2="+strings.Repeat("x", diagConsts.MaxBaggageLength-20))

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		// Check response headers - baggage at max length should be propagated
		assert.NotEmpty(t, rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("multiple baggage items with mixed validity", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1;prop1=val1,invalid;format,key2=value2")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		// Check response headers - only valid baggage should be propagated
		assert.Equal(t, "key1=value1;prop1=val1,key2=value2", rr.Header().Get(diagConsts.BaggageHeader))
	})

	t.Run("multiple, separate baggage headers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Add(diagConsts.BaggageHeader, "key1=value1")
		req.Header.Add(diagConsts.BaggageHeader, "key2=value2")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		// Verify the baggage headers were combined
		assert.Equal(t, "key1=value1,key2=value2", rr.Header().Get(diagConsts.BaggageHeader))
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
