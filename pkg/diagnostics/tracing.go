// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"

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

// FromContext returns the SpanContext stored in a context, or nil if there isn't one.
func FromContext(ctx context.Context) trace.SpanContext {
	sc, _ := ctx.Value(DaprTraceContextKey{}).(trace.SpanContext)
	return sc
}

func startTracingSpanInternal(ctx context.Context, uri, samplingRate string, spanKind int) (context.Context, *trace.Span) {
	var span *trace.Span
	name := createSpanName(uri)

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

func createSpanName(name string) string {
	i := strings.Index(name, "/invoke/")
	if i > 0 {
		j := strings.Index(name[i+8:], "/")
		if j > 0 {
			return name[i+8 : i+8+j]
		}
	}
	return name
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

// UpdateSpanPairStatusesFromError updates tracer span statuses based on error object
func UpdateSpanPairStatusesFromError(span *trace.Span, err error, method string) {
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
