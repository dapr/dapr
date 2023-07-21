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

package utils

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestSpanFromContext(t *testing.T) {
	t.Run("not nil span", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodGet, "http://test.local/method", nil)
		var sp trace.Span
		AddSpanToRequest(r, sp)

		assert.NotNil(t, SpanFromContext(r.Context()))
	})

	t.Run("nil span", func(t *testing.T) {
		r, _ := http.NewRequest(http.MethodGet, "http://test.local/method", nil)
		AddSpanToRequest(r, nil)
		sp := SpanFromContext(r.Context())
		expectedType := "trace.noopSpan"
		gotType := reflect.TypeOf(sp).String()
		assert.Equal(t, expectedType, gotType)
	})

	t.Run("not nil span for context", func(t *testing.T) {
		ctx := context.Background()
		exp := newOtelFakeExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
		tracer := tp.Tracer("dapr-diagnostics-utils-tests")
		ctx, sp := tracer.Start(ctx, "testSpan", trace.WithSpanKind(trace.SpanKindClient))
		expectedTraceID := sp.SpanContext().TraceID()
		expectedSpanID := sp.SpanContext().SpanID()
		newCtx := trace.ContextWithSpan(ctx, sp)
		gotSp := SpanFromContext(newCtx)
		assert.NotNil(t, gotSp)
		assert.Equal(t, expectedTraceID, gotSp.SpanContext().TraceID())
		assert.Equal(t, expectedSpanID, gotSp.SpanContext().SpanID())
	})

	t.Run("nil span for context", func(t *testing.T) {
		ctx := context.Background()
		exp := newOtelFakeExporter()
		_ = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
		newCtx := trace.ContextWithSpan(ctx, nil)
		sp := SpanFromContext(newCtx)
		expectedType := "trace.noopSpan"
		gotType := reflect.TypeOf(sp).String()
		assert.Equal(t, expectedType, gotType)
	})

	t.Run("nil", func(t *testing.T) {
		ctx := context.Background()
		exp := newOtelFakeExporter()
		_ = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
		newCtx := trace.ContextWithSpan(ctx, nil)
		sp := SpanFromContext(newCtx)
		expectedType := "trace.noopSpan"
		gotType := reflect.TypeOf(sp).String()
		assert.Equal(t, expectedType, gotType)
	})
}

// otelFakeExporter implements an open telemetry span exporter that does nothing.
type otelFakeExporter struct{}

// newOtelFakeExporter returns a otelFakeExporter
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
