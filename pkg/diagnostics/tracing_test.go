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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"

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
}

func TestStartInternalCallbackSpan(t *testing.T) {
	exp := newOtelFakeExporter()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	t.Run("traceparent is provided and sampling is enabled", func(t *testing.T) {
		traceSpec := config.TracingSpec{SamplingRate: "1"}

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
		assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", fmt.Sprintf("%x", traceID[:]))
		assert.NotEqual(t, "00f067aa0ba902b7", fmt.Sprintf("%x", spanID[:]))
	})

	t.Run("traceparent is provided but sampling is disabled", func(t *testing.T) {
		traceSpec := config.TracingSpec{SamplingRate: "0"}

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
}

// This test would allow us to know when the span attribute keys are
// modified in go.opentelemetry.io/otel/semconv library, and thus in
// the spec.
func TestOtelConventionStrings(t *testing.T) {
	assert.Equal(t, "db.system", dbSystemSpanAttributeKey)
	assert.Equal(t, "db.name", dbNameSpanAttributeKey)
	assert.Equal(t, "db.statement", dbStatementSpanAttributeKey)
	assert.Equal(t, "db.connection_string", dbConnectionStringSpanAttributeKey)
	assert.Equal(t, "topic", messagingDestinationTopicKind)
	assert.Equal(t, "messaging.system", messagingSystemSpanAttributeKey)
	assert.Equal(t, "messaging.destination", messagingDestinationSpanAttributeKey)
	assert.Equal(t, "messaging.destination_kind", messagingDestinationKindSpanAttributeKey)
	assert.Equal(t, "rpc.service", gRPCServiceSpanAttributeKey)
	assert.Equal(t, "net.peer.name", netPeerNameSpanAttributeKey)
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
