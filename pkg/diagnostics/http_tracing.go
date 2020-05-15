// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
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

// SetTracingInHTTPMiddleware sets the trace context or starts the trace client span based on request
func SetTracingInHTTPMiddleware(next fasthttp.RequestHandler, appID string, spec config.TracingSpec) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		sc := GetSpanContextFromRequestContext(ctx, spec)
		path := string(ctx.Request.URI().Path())
		method := ctx.Request.Header.Method()

		if isHealthzRequest(path) {
			SpanContextToRequest(sc, &ctx.Request)
			next(ctx)
		} else {
			spanName := fmt.Sprintf("%s:%s", method, path)

			newCtx := NewContext((context.Context)(ctx), sc)
			_, span := StartTracingClientSpanFromHTTPContext(newCtx, &ctx.Request, spanName, spec)
			SpanContextToRequest(span.SpanContext(), &ctx.Request)

			next(ctx)

			UpdateSpanStatus(span, spanName, ctx.Response.StatusCode())
			defer span.End()
		}
	}
}

// StartTracingClientSpanFromHTTPContext creates a client span before invoking http method call
func StartTracingClientSpanFromHTTPContext(ctx context.Context, req *fasthttp.Request, method string, spec config.TracingSpec) (context.Context, *trace.Span) {
	var span *trace.Span
	ctx, span = startTracingSpanInternal(ctx, method, spec.SamplingRate, trace.SpanKindClient)

	addAnnotationsToSpan(req, span)

	return ctx, span
}

func GetSpanContextFromRequestContext(ctx *fasthttp.RequestCtx, spec config.TracingSpec) trace.SpanContext {
	spanContext, ok := SpanContextFromRequest(&ctx.Request)

	if !ok {
		spanContext = GetDefaultSpanContext(spec)
	}

	return spanContext
}

// SpanContextFromRequest extracts a span context from incoming requests.
func SpanContextFromRequest(req *fasthttp.Request) (sc trace.SpanContext, ok bool) {
	h, ok := getRequestHeader(req, traceparentHeader)
	if !ok {
		return trace.SpanContext{}, false
	}

	sc, ok = SpanContextFromString(h)

	if ok {
		sc.Tracestate = tracestateFromRequest(req)
	}
	return sc, ok
}

// SpanContextToRequest modifies the given request to include traceparent and tracestate headers.
func SpanContextToRequest(sc trace.SpanContext, req *fasthttp.Request) {
	h := SpanContextToString(sc)
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

func isHealthzRequest(name string) bool {
	return strings.Contains(name, "/healthz")
}

// UpdateSpanStatus updates trace span status based on response code
func UpdateSpanStatus(span *trace.Span, spanName string, code int) {
	if span != nil {
		span.SetStatus(trace.Status{
			Code:    projectStatusCode(code),
			Message: fmt.Sprintf("method %s status - %s", spanName, strconv.Itoa(code)),
		})
	}
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
