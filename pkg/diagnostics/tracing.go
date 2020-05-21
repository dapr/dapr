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
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dapr/dapr/pkg/config"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"

	"go.opencensus.io/trace"
	"google.golang.org/grpc/metadata"
)

type DaprTraceContextKey struct{}

const (
	daprHeaderPrefix = "dapr-"
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

	// Only generating TraceID. SpanID is not generated as there is no span started in the middleware.
	spanContext.TraceID = gen.NewTraceID()

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

func projectStatusCode(code int) int32 {
	switch code {
	case 200:
		return trace.StatusCodeOK
	case 201:
		return trace.StatusCodeOK
	case 400:
		return trace.StatusCodeInvalidArgument
	case 500:
		return trace.StatusCodeInternal
	case 404:
		return trace.StatusCodeNotFound
	case 403:
		return trace.StatusCodePermissionDenied
	default:
		return int32(code)
	}
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

// UpdateSpanStatusFromError updates tracer span status based on error object
func UpdateSpanStatusFromError(span *trace.Span, err error, method string) {
	if span == nil {
		return
	}

	if err != nil {
		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeInternal,
			Message: fmt.Sprintf("method %s failed - %s", method, err.Error()),
		})
	} else {
		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeOK,
			Message: fmt.Sprintf("method %s succeeded", method),
		})
	}
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
