// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/tracestate"
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
		sc := trace.SpanContext{
			TraceID:      trace.TraceID{75, 249, 47, 53, 119, 179, 77, 166, 163, 206, 146, 157, 14, 14, 71, 54},
			SpanID:       trace.SpanID{0, 240, 103, 170, 11, 169, 2, 183},
			TraceOptions: trace.TraceOptions(1)}
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
		entry := tracestate.Entry{Key: "key", Value: "value"}
		ts, _ := tracestate.New(nil, entry)
		sc := trace.SpanContext{}
		sc.Tracestate = ts
		got := TraceStateToW3CString(sc)
		assert.Equal(t, "key=value", got)
	})
}
