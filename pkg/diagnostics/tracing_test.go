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
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestSpanContextToW3CString(t *testing.T) {
	t.Run("empty SpanContext", func(t *testing.T) {
		expected := "00-00000000000000000000000000000000-0000000000000000-00"
		sc := trace.SpanContext{}
		got := SpanContextToW3CString(sc)
		assert.Equal(t, expected, got)
	})
	t.Run("valid SpanContext", func(t *testing.T) {
		expected := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		scConfig := trace.SpanContextConfig{
			TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
			SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
			TraceFlags: trace.TraceFlags(1),
		}
		sc := trace.NewSpanContext(scConfig)
		got := SpanContextToW3CString(sc)
		assert.Equal(t, expected, got)
	})
}

func TestTraceStateToW3CString(t *testing.T) {
	t.Run("empty Tracestate", func(t *testing.T) {
		sc := trace.SpanContext{}
		got := TraceStateToW3CString(sc)
		assert.Empty(t, got)
	})
	t.Run("valid Tracestate", func(t *testing.T) {
		ts := trace.TraceState{}
		ts, _ = ts.Insert("key", "value")
		sc := trace.SpanContext{}
		sc = sc.WithTraceState(ts)
		got := TraceStateToW3CString(sc)
		assert.Equal(t, "key=value", got)
	})
}

func TestSpanContextFromW3CString(t *testing.T) {
	t.Run("empty SpanContext", func(t *testing.T) {
		sc := "00-00000000000000000000000000000000-0000000000000000-00"
		expected := trace.SpanContext{}
		got, _ := SpanContextFromW3CString(sc)
		assert.Equal(t, expected, got)
	})
	t.Run("valid SpanContext", func(t *testing.T) {
		sc := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		scConfig := trace.SpanContextConfig{
			TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
			SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
			TraceFlags: trace.TraceFlags(1),
		}
		expected := trace.NewSpanContext(scConfig)
		got, _ := SpanContextFromW3CString(sc)
		assert.Equal(t, expected, got)
	})
}

func TestTraceStateFromW3CString(t *testing.T) {
	t.Run("empty Tracestate", func(t *testing.T) {
		ts := trace.TraceState{}
		sc := trace.SpanContext{}
		sc = sc.WithTraceState(ts)
		scText := TraceStateToW3CString(sc)
		got := TraceStateFromW3CString(scText)
		assert.Equal(t, ts, *got)
	})
	t.Run("valid Tracestate", func(t *testing.T) {
		ts := trace.TraceState{}
		ts, _ = ts.Insert("key", "value")
		sc := trace.SpanContext{}
		sc = sc.WithTraceState(ts)
		scText := TraceStateToW3CString(sc)
		got := TraceStateFromW3CString(scText)
		assert.Equal(t, ts, *got)
	})
	t.Run("invalid Tracestate", func(t *testing.T) {
		ts := trace.TraceState{}
		// A non-parsable tracestate should equate back to an empty one.
		got := TraceStateFromW3CString("bad tracestate")
		assert.Equal(t, ts, *got)
	})
}

func TestStartInternalCallbackSpan(t *testing.T) {
	exp := newOtelFakeExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	t.Run("traceparent is provided and sampling is enabled", func(t *testing.T) {
		traceSpec := &config.TracingSpec{SamplingRate: "1"}

		scConfig := trace.SpanContextConfig{
			TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
			SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
			TraceFlags: trace.TraceFlags(1),
		}
		parent := trace.NewSpanContext(scConfig)

		ctx := context.Background()

		_, gotSp := StartInternalCallbackSpan(ctx, "testSpanName", parent, traceSpec)
		sc := gotSp.SpanContext()
		traceID := sc.TraceID()
		spanID := sc.SpanID()
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", hex.EncodeToString(traceID[:]))
		assert.NotEqual(t, "00f067aa0ba902b7", hex.EncodeToString(spanID[:]))
	})

	t.Run("traceparent is provided with sampling flag = 1 but sampling is disabled", func(t *testing.T) {
		traceSpec := &config.TracingSpec{SamplingRate: "0"}

		scConfig := trace.SpanContextConfig{
			TraceID:    trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
			SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
			TraceFlags: trace.TraceFlags(1),
		}
		parent := trace.NewSpanContext(scConfig)

		ctx := context.Background()

		ctx, gotSp := StartInternalCallbackSpan(ctx, "testSpanName", parent, traceSpec)
		assert.Nil(t, gotSp)
		assert.NotNil(t, ctx)
	})

	t.Run("traceparent is provided with sampling flag = 0 and sampling is enabled (but not P=1.00)", func(t *testing.T) {
		// We use a fixed seed for the RNG so we can use an exact number here
		const expectSampled = 1051
		const numTraces = 100000
		sampledCount := runTraces(t, "test_trace", numTraces, "0.01", 0)
		require.Equal(t, expectSampled, sampledCount, "Expected to sample %d traces but sampled %d", expectSampled, sampledCount)
		require.Less(t, sampledCount, numTraces, "Expected to sample fewer than the total number of traces, but sampled all of them!")
	})

	t.Run("traceparent is provided with sampling flag = 0 and sampling is enabled (and P=1.00)", func(t *testing.T) {
		const numTraces = 1000
		sampledCount := runTraces(t, "test_trace", numTraces, "1.00", 0)
		require.Equal(t, numTraces, sampledCount, "Expected to sample all traces (%d) but only sampled %d", numTraces, sampledCount)
	})

	t.Run("traceparent is provided with sampling flag = 1 and sampling is enabled (but not P=1.00)", func(t *testing.T) {
		const numTraces = 1000
		sampledCount := runTraces(t, "test_trace", numTraces, "0.00001", 1)
		require.Equal(t, numTraces, sampledCount, "Expected to sample all traces (%d) but only sampled %d", numTraces, sampledCount)
	})
}

func runTraces(t *testing.T, testName string, numTraces int, samplingRate string, parentTraceFlag int) int {
	d := NewDaprTraceSampler(samplingRate)
	tracerOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(d),
	}

	tp := sdktrace.NewTracerProvider(tracerOptions...)

	tracerName := fmt.Sprintf("%s_%s", testName, samplingRate)
	otel.SetTracerProvider(tp)
	testTracer := otel.Tracer(tracerName)

	// This is taken from otel's tests for the ratio sampler so we can generate IDs
	idg := defaultIDGenerator()
	sampledCount := 0

	for i := 0; i < numTraces; i++ {
		traceID, _ := idg.NewIDs(context.Background())
		scConfig := trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
			TraceFlags: trace.TraceFlags(parentTraceFlag),
		}

		parent := trace.NewSpanContext(scConfig)

		ctx := context.Background()
		ctx = trace.ContextWithRemoteSpanContext(ctx, parent)
		ctx, span := testTracer.Start(ctx, "testTraceSpan", trace.WithSpanKind(trace.SpanKindClient))
		assert.NotNil(t, span)
		assert.NotNil(t, ctx)

		if span.SpanContext().IsSampled() {
			sampledCount += 1
		}
	}

	return sampledCount
}

// This test would allow us to know when the span attribute keys are
// modified in go.opentelemetry.io/otel/semconv library, and thus in
// the spec.
func TestOtelConventionStrings(t *testing.T) {
	assert.Equal(t, "db.system", diagConsts.DBSystemSpanAttributeKey)
	assert.Equal(t, "db.name", diagConsts.DBNameSpanAttributeKey)
	assert.Equal(t, "db.statement", diagConsts.DBStatementSpanAttributeKey)
	assert.Equal(t, "db.connection_string", diagConsts.DBConnectionStringSpanAttributeKey)
	assert.Equal(t, "topic", diagConsts.MessagingDestinationTopicKind)
	assert.Equal(t, "messaging.system", diagConsts.MessagingSystemSpanAttributeKey)
	assert.Equal(t, "messaging.destination", diagConsts.MessagingDestinationSpanAttributeKey)
	assert.Equal(t, "messaging.destination_kind", diagConsts.MessagingDestinationKindSpanAttributeKey)
	assert.Equal(t, "rpc.service", diagConsts.GrpcServiceSpanAttributeKey)
	assert.Equal(t, "net.peer.name", diagConsts.NetPeerNameSpanAttributeKey)
}

// Otel Fake Exporter implements an open telemetry span exporter that does nothing.
type otelFakeExporter struct{}

// newOtelFakeExporter returns an Open Telemetry Fake Span Exporter

func newOtelFakeExporter() *otelFakeExporter {
	return &otelFakeExporter{}
}

// ExportSpans implements the open telemetry span exporter interface.
func (e *otelFakeExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return nil
}

// Shutdown implements the open telemetry span exporter interface.
func (e *otelFakeExporter) Shutdown(ctx context.Context) error {
	return nil
}

// Otel Fake Span Processor implements an open telemetry span processor that calls a call back in the OnEnd method.
type otelFakeSpanProcessor struct {
	cb func(s sdktrace.ReadOnlySpan)
}

// newOtelFakeSpanProcessor returns an Open Telemetry Fake Span Processor
func newOtelFakeSpanProcessor(f func(s sdktrace.ReadOnlySpan)) *otelFakeSpanProcessor {
	return &otelFakeSpanProcessor{
		cb: f,
	}
}

// OnStart implements the SpanProcessor interface.
func (o *otelFakeSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
}

// OnEnd  implements the SpanProcessor interface and calls the callback function provided on init
func (o *otelFakeSpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	o.cb(s)
}

// Shutdown implements the SpanProcessor interface.
func (o *otelFakeSpanProcessor) Shutdown(ctx context.Context) error {
	return nil
}

// ForceFlush implements the SpanProcessor interface.
func (o *otelFakeSpanProcessor) ForceFlush(ctx context.Context) error {
	return nil
}

// This was taken from the otel testing to generate IDs
// origin: go.opentelemetry.io/otel/sdk@v1.11.1/trace/id_generator.go
// Copyright: The OpenTelemetry Authors
// License (Apache 2.0): https://github.com/open-telemetry/opentelemetry-go/blob/sdk/v1.11.1/LICENSE

// IDGenerator allows custom generators for TraceID and SpanID.
type IDGenerator interface {
	// DO NOT CHANGE: any modification will not be backwards compatible and
	// must never be done outside of a new major release.

	// NewIDs returns a new trace and span ID.
	NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID)
	// DO NOT CHANGE: any modification will not be backwards compatible and
	// must never be done outside of a new major release.

	// NewSpanID returns a ID for a new span in the trace with traceID.
	NewSpanID(ctx context.Context, traceID trace.TraceID) trace.SpanID
	// DO NOT CHANGE: any modification will not be backwards compatible and
	// must never be done outside of a new major release.
}

type randomIDGenerator struct {
	sync.Mutex
	randSource *rand.Rand
}

var _ IDGenerator = &randomIDGenerator{}

// NewSpanID returns a non-zero span ID from a randomly-chosen sequence.
func (gen *randomIDGenerator) NewSpanID(ctx context.Context, traceID trace.TraceID) trace.SpanID {
	gen.Lock()
	defer gen.Unlock()
	sid := trace.SpanID{}
	_, _ = gen.randSource.Read(sid[:])
	return sid
}

// NewIDs returns a non-zero trace ID and a non-zero span ID from a
// randomly-chosen sequence.
func (gen *randomIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	gen.Lock()
	defer gen.Unlock()
	tid := trace.TraceID{}
	_, _ = gen.randSource.Read(tid[:])
	sid := trace.SpanID{}
	_, _ = gen.randSource.Read(sid[:])
	return tid, sid
}

func defaultIDGenerator() IDGenerator {
	gen := &randomIDGenerator{
		// Use a fixed seed to make the tests deterministic.
		randSource: rand.New(rand.NewSource(1)), //nolint:gosec
	}
	return gen
}

func TestTraceIDAndStateFromSpan(t *testing.T) {
	t.Run("non-empty span, id and state are not empty", func(t *testing.T) {
		idg := defaultIDGenerator()
		traceID, _ := idg.NewIDs(context.Background())
		scConfig := trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
			TraceFlags: 1,
		}
		ts := trace.TraceState{}
		ts, _ = ts.Insert("key", "value")

		scConfig.TraceState = ts
		parent := trace.NewSpanContext(scConfig)

		ctx := context.Background()
		ctx = trace.ContextWithRemoteSpanContext(ctx, parent)
		_, span := tracer.Start(ctx, "testTraceSpan", trace.WithSpanKind(trace.SpanKindClient))

		id, state := TraceIDAndStateFromSpan(span)
		assert.NotEmpty(t, id)
		assert.NotEmpty(t, state)
	})

	t.Run("empty span, id and state are empty", func(t *testing.T) {
		span := trace.SpanFromContext(context.Background())
		id, state := TraceIDAndStateFromSpan(span)
		assert.Empty(t, id)
		assert.Empty(t, state)
	})

	t.Run("nil span, id and state are empty", func(t *testing.T) {
		id, state := TraceIDAndStateFromSpan(nil)
		assert.Empty(t, id)
		assert.Empty(t, state)
	})
}
