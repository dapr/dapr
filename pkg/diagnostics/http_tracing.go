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
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/tracestate"
)

const (
	supportedVersion  = 0
	maxVersion        = 254
	maxTracestateLen  = 512
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
	trimOWSRegexFmt   = `^[\x09\x20]*(.*[^\x20\x09])[\x09\x20]*$`
)

var trimOWSRegExp = regexp.MustCompile(trimOWSRegexFmt)

type contextKey struct{}

// StartClientSpanTracing creates a tracing span from a http request
func StartClientSpanTracing(ctx context.Context, r *fasthttp.Request, spec config.TracingSpec) (context.Context, *trace.Span) {
	corID := string(r.Header.Peek(CorrelationID))
	uri := string(r.Header.RequestURI())
	var span *trace.Span

	ctx, span = startTracingSpan(ctx, corID, uri, spec.SamplingRate, trace.SpanKindClient)
	addAnnotationsToSpan(r, span)

	return ctx, span
}

// StartServerSpanTracing plugs tracing into http middleware pipeline
func StartServerSpanTracing(spec config.TracingSpec, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		corID := string(ctx.Request.Header.Peek(CorrelationID))
		uri := string(ctx.Path())
		_, span := startTracingSpan(ctx, corID, uri, spec.SamplingRate, trace.SpanKindServer)

		addAnnotationsToSpan(&ctx.Request, span)
		defer span.End()

		// Pass on the correlation id further in the request header
		ctx.Request.Header.Set(CorrelationID, SerializeSpanContext(span.SpanContext()))

		next(ctx)
		UpdateSpanStatus(span, &ctx.Response)
	}
}

// SetTracingSpanContext sets the trace spancontext in the request context
func SetTracingSpanContext(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		sc := GetSpanContextFromRequestContext(ctx)
		SpanContextToRequest(sc, &ctx.Request)
		next(ctx)
	}
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

func WithSpanContext(ctx context.Context) context.Context {
	spanContext := FromContext(ctx)

	gen := tracingConfig.Load().(*traceIDGenerator)

	if (spanContext == trace.SpanContext{}) {
		spanContext = trace.SpanContext{}
		spanContext.TraceID = gen.NewTraceID()
		spanContext.SpanID = gen.NewSpanID()
	} else {
		spanContext.SpanID = gen.NewSpanID()
	}

	return NewContext(ctx, spanContext)
}

func GetSpanContextFromRequestContext(ctx *fasthttp.RequestCtx) trace.SpanContext {
	spanContext, ok := SpanContextFromRequest(&ctx.Request)

	gen := tracingConfig.Load().(*traceIDGenerator)

	if !ok {
		spanContext = trace.SpanContext{}
		spanContext.TraceID = gen.NewTraceID()
		spanContext.SpanID = gen.NewSpanID()
	} else {
		spanContext.SpanID = gen.NewSpanID()
	}

	return spanContext
}

// FromContext returns the SpanContext stored in a context, or empty if there isn't one.
func FromContext(ctx context.Context) trace.SpanContext {
	s, _ := ctx.Value(contextKey{}).(string)
	return DeserializeSpanContext(s)
}

// NewContext returns a new context with the given SpanContext attached in serialized value
func NewContext(parent context.Context, spanContext trace.SpanContext) context.Context {
	return context.WithValue(parent, contextKey{}, SerializeSpanContext(spanContext))
}

func startTracingSpan(ctx context.Context, corID, uri, samplingRate string, spanKind int) (context.Context, *trace.Span) {
	var span *trace.Span
	name := createSpanName(uri)

	rate := diag_utils.GetTraceSamplingRate(samplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	kindOption := trace.WithSpanKind(spanKind)

	if corID != "" {
		sc := DeserializeSpanContext(corID)
		// Note that if parent span context is provided which is sc in this case then ctx will be ignored
		ctx, span = trace.StartSpanWithRemoteParent(ctx, name, sc, kindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(ctx, name, kindOption, probSamplerOption)
	}

	return ctx, span
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

	var entries []tracestate.Entry
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
