// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/valyala/fasthttp"

	"go.opencensus.io/trace"
	"google.golang.org/grpc/metadata"
)

type DaprTraceContextKey struct{}

type apiComponent struct {
	componentType  string
	componentValue string
}

const (
	daprHeaderPrefix = "dapr-"

	// span attribute keys
	// Reference trace semantics https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	dbTypeSpanAttributeKey      = "db.type"
	dbInstanceSpanAttributeKey  = "db.instance"
	dbStatementSpanAttributeKey = "db.statement"
	dbURLSpanAttributeKey       = "db.url"

	httpMethodSpanAttributeKey     = "http.method"
	httpURLSpanAttributeKey        = "http.url"
	httpStatusCodeSpanAttributeKey = "http.status_code"
	httpStatusTextSpanAttributeKey = "http.status_text"

	messagingDestinationKind                 = "topic"
	messagingSystemSpanAttributeKey          = "messaging.system"
	messagingDestinationSpanAttributeKey     = "messaging.destination"
	messagingDestinationKindSpanAttributeKey = "messaging.destination_kind"

	gRPCServiceSpanAttributeKey = "rpc.service"
	// below attribute is not as per open temetery spec, adding custom dapr attribute to have additional details
	gRPCDaprInstanceSpanAttributeKey = "rpc.dapr.instance"
)

// NewContext returns a new context with the given SpanContext attached.
func NewContext(ctx context.Context, spanContext trace.SpanContext) context.Context {
	return context.WithValue(ctx, DaprTraceContextKey{}, spanContext)
}

// SpanContextToString returns the SpanContext string representation
func SpanContextToString(sc trace.SpanContext) string {
	return fmt.Sprintf("%x-%x-%x-%x",
		[]byte{supportedVersion},
		sc.TraceID[:],
		sc.SpanID[:],
		[]byte{byte(sc.TraceOptions)})
}

// SpanContextFromString extracts a span context from given string which got earlier from SpanContextToString format
func SpanContextFromString(h string) (sc trace.SpanContext, ok bool) {
	if h == "" {
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

	return sc, true
}

// FromContext returns the SpanContext stored in a context, or nil if there isn't one.
func FromContext(ctx context.Context) trace.SpanContext {
	sc, _ := ctx.Value(DaprTraceContextKey{}).(trace.SpanContext)
	return sc
}

func startTracingSpanInternal(ctx context.Context, name, samplingRate string, spanKind int) (context.Context, *trace.Span) {
	var span *trace.Span

	rate := diag_utils.GetTraceSamplingRate(samplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	probSamplerOption := trace.WithSampler(trace.ProbabilitySampler(rate))
	kindOption := trace.WithSpanKind(spanKind)

	sc := FromContext(ctx)

	if (sc != trace.SpanContext{}) {
		// Note that if parent span context is provided which is sc in this case then ctx will be ignored
		ctx, span = trace.StartSpanWithRemoteParent(ctx, name, sc, kindOption, probSamplerOption)
	} else {
		ctx, span = trace.StartSpan(ctx, name, kindOption, probSamplerOption)
	}

	return ctx, span
}

// GetDefaultSpanContext returns default span context when not provided by the client
func GetDefaultSpanContext(spec config.TracingSpec) trace.SpanContext {
	spanContext := trace.SpanContext{}

	gen := tracingConfig.Load().(*traceIDGenerator)

	spanContext.TraceID = gen.NewTraceID()
	spanContext.SpanID = gen.NewSpanID()

	rate := diag_utils.GetTraceSamplingRate(spec.SamplingRate)

	// TODO : Continue using ProbabilitySampler till Go SDK starts supporting RateLimiting sampler
	sampler := trace.ProbabilitySampler(rate)
	sampled := sampler(trace.SamplingParameters{
		ParentContext:   trace.SpanContext{},
		TraceID:         spanContext.TraceID,
		SpanID:          spanContext.SpanID,
		HasRemoteParent: false}).Sample

	if sampled {
		spanContext.TraceOptions = 1
	}

	return spanContext
}

func extractDaprMetadata(ctx context.Context) map[string][]string {
	daprMetadata := make(map[string][]string)
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return daprMetadata
	}

	for k, v := range md {
		k = strings.ToLower(k)
		if strings.HasPrefix(k, daprHeaderPrefix) {
			daprMetadata[k] = v
		}
	}

	return daprMetadata
}

func getSpanAttributesMapFromHTTPContext(ctx *fasthttp.RequestCtx) map[string]string {
	// Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	route := string(ctx.Request.URI().Path())
	method := string(ctx.Request.Header.Method())
	uri := ctx.Request.URI().String()
	statusCode := ctx.Response.StatusCode()
	r := getAPIComponent(route)
	return GetSpanAttributesMapFromHTTP(r.componentType, r.componentValue, method, route, uri, statusCode)
}

// GetSpanAttributesMap builds the span trace attributes map for HTTP calls based on given parameters as per open-telemetry specs
func GetSpanAttributesMapFromHTTP(componentType, componentValue, method, route, uri string, statusCode int) map[string]string {
	// Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/tree/master/specification/trace/semantic_conventions
	m := make(map[string]string)
	switch componentType {
	case "state", "secrets", "bindings":
		m[dbTypeSpanAttributeKey] = componentType
		m[dbInstanceSpanAttributeKey] = componentValue
		// TODO: not possible currently to get the route {state_store} , so using path instead of route
		m[dbStatementSpanAttributeKey] = fmt.Sprintf("%s %s", method, route)
		m[dbURLSpanAttributeKey] = route
	case "invoke", "actors":
		m[httpMethodSpanAttributeKey] = method
		m[httpURLSpanAttributeKey] = uri
		code := invokev1.CodeFromHTTPStatus(statusCode)
		m[httpStatusCodeSpanAttributeKey] = strconv.Itoa(statusCode)
		m[httpStatusTextSpanAttributeKey] = code.String()
	case "publish":
		m[messagingSystemSpanAttributeKey] = componentType
		m[messagingDestinationSpanAttributeKey] = componentValue
		m[messagingDestinationKindSpanAttributeKey] = messagingDestinationKind
	}
	return m
}

// AddAttributesToSpan adds the given attributes in the span
func AddAttributesToSpan(span *trace.Span, attributes map[string]string) {
	if span != nil {
		for k, v := range attributes {
			if v != "" {
				span.AddAttributes(trace.StringAttribute(k, v))
			}
		}
	}
}

func getAPIComponent(apiPath string) apiComponent {
	// Dapr API reference : https://github.com/dapr/docs/tree/master/reference/api
	// example : apiPath /v1.0/state/statestore
	if apiPath == "" {
		return apiComponent{}
	}

	p := apiPath
	if p[0] == '/' {
		p = apiPath[1:]
	}

	// Split up to 4 delimiters in 'v1.0/state/statestore/key' to get component api type and value
	var tokens = strings.SplitN(p, "/", 4)
	if len(tokens) < 3 {
		return apiComponent{}
	}

	// return 'state', 'statestore' from the parsed tokens in apiComponent type
	return apiComponent{componentType: tokens[1], componentValue: tokens[2]}
}

// GetSpanAttributesMapFromGRPC builds the span trace attributes map for gRPC calls based on given parameters as per open-telemetry specs
func GetSpanAttributesMapFromGRPC(req interface{}, rpcMethod string) map[string]string {
	// RPC Span Attribute reference https://github.com/open-telemetry/opentelemetry-specification/blob/master/specification/trace/semantic_conventions/rpc.md
	// gRPC method /package.service/method
	var serviceName string

	p := rpcMethod
	if p[0] == '/' {
		p = rpcMethod[1:]
	}

	// Split up to 3 delimiters in '/package.service/method'
	// example for internal call : "/dapr.proto.internals.v1.ServiceInvocation/CallLocal"
	// example for non-internal call : "/dapr.proto.runtime.v1.Dapr/InvokeService"
	internalCall := strings.Contains(p, "dapr.proto.internals.")

	tokens := strings.SplitN(p, "/", 3)
	if len(tokens) >= 2 {
		if internalCall {
			if t := strings.SplitN(tokens[0], ".", 5); len(t) >= 4 {
				serviceName = t[4]
			}
		} else {
			// TODO : need to revisit /package.service/method format in runtime calls, currently service name is "Dapr"
			// so instead of taking general "Dapr", taking method name as service name to give better insights
			serviceName = tokens[1]
		}
	} else {
		// default service name to full method name, after removing first "/"
		serviceName = p
	}

	m := make(map[string]string)
	m[gRPCServiceSpanAttributeKey] = serviceName

	// below attribute is not as per open temetery spec, adding custom dapr attribute to have additional details
	m[gRPCDaprInstanceSpanAttributeKey] = extractComponentValueFromGRPCRequest(req)
	return m
}

func extractComponentValueFromGRPCRequest(req interface{}) string {
	if req == nil {
		return ""
	}
	if s, ok := req.(*internalv1pb.InternalInvokeRequest); ok {
		return s.Message.GetMethod()
	}
	if s, ok := req.(*runtimev1pb.PublishEventRequest); ok {
		return s.GetTopic()
	}
	if s, ok := req.(*runtimev1pb.InvokeServiceRequest); ok {
		return s.Message.GetMethod()
	}
	if s, ok := req.(*runtimev1pb.InvokeBindingRequest); ok {
		return s.GetName()
	}
	if s, ok := req.(*runtimev1pb.GetStateRequest); ok {
		return s.GetStoreName()
	}
	if s, ok := req.(*runtimev1pb.GetSecretRequest); ok {
		return s.GetStoreName()
	}
	if s, ok := req.(*runtimev1pb.SaveStateRequest); ok {
		return s.GetStoreName()
	}
	if s, ok := req.(*runtimev1pb.DeleteStateRequest); ok {
		return s.GetStoreName()
	}
	if s, ok := req.(*runtimev1pb.TopicEventRequest); ok {
		return s.GetTopic()
	}
	if s, ok := req.(*runtimev1pb.BindingEventRequest); ok {
		return s.GetName()
	}
	return ""
}

var tracingConfig atomic.Value // access atomically

func init() {
	gen := &traceIDGenerator{}
	// initialize traceID and spanID generators.
	var rngSeed int64
	for _, p := range []interface{}{
		&rngSeed, &gen.traceIDAdd, &gen.nextSpanID, &gen.spanIDInc,
	} {
		binary.Read(crand.Reader, binary.LittleEndian, p)
	}
	gen.traceIDRand = rand.New(rand.NewSource(rngSeed))
	gen.spanIDInc |= 1

	tracingConfig.Store(gen)
}

type traceIDGenerator struct {
	sync.Mutex

	// Please keep these as the first fields
	// so that these 8 byte fields will be aligned on addresses
	// divisible by 8, on both 32-bit and 64-bit machines when
	// performing atomic increments and accesses.
	// See:
	// * https://github.com/census-instrumentation/opencensus-go/issues/587
	// * https://github.com/census-instrumentation/opencensus-go/issues/865
	// * https://golang.org/pkg/sync/atomic/#pkg-note-BUG
	nextSpanID uint64
	spanIDInc  uint64

	traceIDAdd  [2]uint64
	traceIDRand *rand.Rand
}

// NewSpanID returns a non-zero span ID from a randomly-chosen sequence.
func (gen *traceIDGenerator) NewSpanID() [8]byte {
	var id uint64
	for id == 0 {
		id = atomic.AddUint64(&gen.nextSpanID, gen.spanIDInc)
	}
	var sid [8]byte
	binary.LittleEndian.PutUint64(sid[:], id)
	return sid
}

// NewTraceID returns a non-zero trace ID from a randomly-chosen sequence.
// mu should be held while this function is called.
func (gen *traceIDGenerator) NewTraceID() [16]byte {
	var tid [16]byte
	// Construct the trace ID from two outputs of traceIDRand, with a constant
	// added to each half for additional entropy.
	gen.Lock()
	binary.LittleEndian.PutUint64(tid[0:8], gen.traceIDRand.Uint64()+gen.traceIDAdd[0])
	binary.LittleEndian.PutUint64(tid[8:16], gen.traceIDRand.Uint64()+gen.traceIDAdd[1])
	gen.Unlock()
	return tid
}
