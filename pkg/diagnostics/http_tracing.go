// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/dapr/dapr/pkg/config"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/tracestate"
)

// We have leveraged the code from opencensus-go plugin to adhere the w3c trace context.
// Reference : https://github.com/census-instrumentation/opencensus-go/blob/master/plugin/ochttp/propagation/tracecontext/propagation.go
const (
	supportedVersion  = 0
	maxVersion        = 254
	maxTracestateLen  = 512
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
	trimOWSRegexFmt   = `^[\x09\x20]*(.*[^\x20\x09])[\x09\x20]*$`
)

var trimOWSRegExp = regexp.MustCompile(trimOWSRegexFmt)

// SetTracingSpanContextFromHTTPContext sets the trace SpanContext in the request context
func SetTracingSpanContextFromHTTPContext(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		sc := GetSpanContextFromRequestContext(ctx)
		SpanContextToRequest(sc, &ctx.Request)
		next(ctx)
	}
}

// StartTracingClientSpanFromHTTPContext creates a client span before invoking http method call
func StartTracingClientSpanFromHTTPContext(ctx context.Context, req *fasthttp.Request, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpanInternal(ctx, method, spec.SamplingRate, trace.SpanKindClient)

	addAnnotationsToSpan(req, span)

	return ctx, span
}

func GetSpanContextFromRequestContext(ctx *fasthttp.RequestCtx) trace.SpanContext {
	spanContext, ok := SpanContextFromRequest(&ctx.Request)

	gen := tracingConfig.Load().(*traceIDGenerator)

	if !ok {
		spanContext = trace.SpanContext{}
		// Only generating TraceID. SpanID is not generated as there is no span started in the middleware.
		spanContext.TraceID = gen.NewTraceID()

		// Default sampling rate is non zero in Dapr, so that means , sampling is enabled by default
		spanContext.TraceOptions = trace.TraceOptions(1)
	}

	return spanContext
}

// SpanContextFromRequest extracts a span context from incoming requests.
func SpanContextFromRequest(req *fasthttp.Request) (sc trace.SpanContext, ok bool) {
	h, ok := getRequestHeader(req, traceparentHeader)
	if !ok {
		return trace.SpanContext{}, false
	}
	sections := strings.Split(h, "-")
	if len(sections) < 4 {
		return trace.SpanContext{}, false
	}

	if len(sections[0]) != 2 {
		return trace.SpanContext{}, false
	}
	ver, err := hex.DecodeString(sections[0])
	if err != nil {
		return trace.SpanContext{}, false
	}
	version := int(ver[0])
	if version > maxVersion {
		return trace.SpanContext{}, false
	}

	if version == 0 && len(sections) != 4 {
		return trace.SpanContext{}, false
	}

	if len(sections[1]) != 32 {
		return trace.SpanContext{}, false
	}
	tid, err := hex.DecodeString(sections[1])
	if err != nil {
		return trace.SpanContext{}, false
	}
	copy(sc.TraceID[:], tid)

	if len(sections[2]) != 16 {
		return trace.SpanContext{}, false
	}
	sid, err := hex.DecodeString(sections[2])
	if err != nil {
		return trace.SpanContext{}, false
	}
	copy(sc.SpanID[:], sid)

	opts, err := hex.DecodeString(sections[3])
	if err != nil || len(opts) < 1 {
		return trace.SpanContext{}, false
	}
	sc.TraceOptions = trace.TraceOptions(opts[0])

	// Don't allow all zero trace or span ID.
	if sc.TraceID == [16]byte{} || sc.SpanID == [8]byte{} {
		return trace.SpanContext{}, false
	}

	sc.Tracestate = tracestateFromRequest(req)
	return sc, true
}

// SpanContextToRequest modifies the given request to include traceparent and tracestate headers.
func SpanContextToRequest(sc trace.SpanContext, req *fasthttp.Request) {
	h := fmt.Sprintf("%x-%x-%x-%x",
		[]byte{supportedVersion},
		sc.TraceID[:],
		sc.SpanID[:],
		[]byte{byte(sc.TraceOptions)})
	req.Header.Set(traceparentHeader, h)
	tracestateToRequest(sc, req)
}

func addAnnotationsToSpan(req *fasthttp.Request, span *trace.Span) {
	req.Header.VisitAll(func(key []byte, value []byte) {
		headerKey := string(key)
		headerKey = strings.ToLower(headerKey)
		if strings.HasPrefix(headerKey, daprHeaderPrefix) {
			span.AddAttributes(trace.StringAttribute(headerKey, string(value)))
		}
	})
}

// UpdateSpanStatus updates trace span status based on HTTP response
func UpdateSpanStatus(span *trace.Span, resp *fasthttp.Response) {
	span.SetStatus(trace.Status{
		Code:    projectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})
}

func getRequestHeader(req *fasthttp.Request, name string) (string, bool) {
	s := string(req.Header.Peek(textproto.CanonicalMIMEHeaderKey(name)))
	if s == "" {
		return "", false
	}

	return s, true
}

func tracestateFromRequest(req *fasthttp.Request) *tracestate.Tracestate {
	h, _ := getRequestHeader(req, tracestateHeader)
	if h == "" {
		return nil
	}

	entries := make([]tracestate.Entry, 0, len(h))
	pairs := strings.Split(h, ",")
	hdrLenWithoutOWS := len(pairs) - 1 // Number of commas
	for _, pair := range pairs {
		matches := trimOWSRegExp.FindStringSubmatch(pair)
		if matches == nil {
			return nil
		}
		pair = matches[1]
		hdrLenWithoutOWS += len(pair)
		if hdrLenWithoutOWS > maxTracestateLen {
			return nil
		}
		kv := strings.Split(pair, "=")
		if len(kv) != 2 {
			return nil
		}
		entries = append(entries, tracestate.Entry{Key: kv[0], Value: kv[1]})
	}
	ts, err := tracestate.New(nil, entries...)
	if err != nil {
		return nil
	}

	return ts
}

func tracestateToRequest(sc trace.SpanContext, req *fasthttp.Request) {
	var pairs = make([]string, 0, len(sc.Tracestate.Entries()))
	if sc.Tracestate != nil {
		for _, entry := range sc.Tracestate.Entries() {
			pairs = append(pairs, strings.Join([]string{entry.Key, entry.Value}, "="))
		}
		h := strings.Join(pairs, ",")

		if h != "" && len(h) <= maxTracestateLen {
			req.Header.Set(tracestateHeader, h)
		}
	}
}
