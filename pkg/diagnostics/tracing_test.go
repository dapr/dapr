// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"testing"

	"github.com/dapr/dapr/pkg/config"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
)

func TestTraceSpanFromFastHTTPRequest(t *testing.T) {
	req := getTestHTTPRequest()
	spec := config.TracingSpec{Enabled: true}

	TraceSpanFromFastHTTPRequest(req, spec)
}

func TestTraceSpanFromFastHTTPContext(t *testing.T) {
	req := getTestHTTPRequest()
	spec := config.TracingSpec{Enabled: true}
	ctx := &fasthttp.RequestCtx{Request: *req}

	TraceSpanFromFastHTTPContext(ctx, spec)
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

	corID := SerializeSpanContext(sc)
	req.Header.Set(CorrelationID, corID)

	return req
}
