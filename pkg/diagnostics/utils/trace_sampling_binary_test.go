/*
Copyright 2026 The Dapr Authors
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
	"testing"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/otel/trace"
)

func TestGetTraceSamplingRate(t *testing.T) {
	tests := []struct {
		name string
		rate string
		want float64
	}{
		{name: "valid rate", rate: "0.5", want: 0.5},
		{name: "zero rate", rate: "0", want: 0},
		{name: "invalid rate falls back to default", rate: "not-a-number", want: defaultSamplingRate},
		{name: "empty rate falls back to default", rate: "", want: defaultSamplingRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetTraceSamplingRate(tt.rate))
		})
	}
}

func TestIsTracingEnabled(t *testing.T) {
	tests := []struct {
		name string
		rate string
		want bool
	}{
		{name: "nonzero rate is enabled", rate: "1", want: true},
		{name: "zero rate is disabled", rate: "0", want: false},
		{name: "invalid rate falls back to default, enabled", rate: "garbage", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsTracingEnabled(tt.rate))
		})
	}
}

func TestBinaryFromSpanContext(t *testing.T) {
	t.Run("empty span context returns nil", func(t *testing.T) {
		assert.Nil(t, BinaryFromSpanContext(trace.SpanContext{}))
	})

	t.Run("valid span context returns 29 bytes", func(t *testing.T) {
		traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
		assert.NoError(t, err)
		spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
		assert.NoError(t, err)
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		})

		b := BinaryFromSpanContext(sc)
		assert.Len(t, b, 29)
		assert.Equal(t, byte(0), b[0])
		assert.Equal(t, byte(0), b[1])
		assert.Equal(t, traceID[:], b[2:18])
		assert.Equal(t, byte(1), b[18])
		assert.Equal(t, spanID[:], b[19:27])
		assert.Equal(t, byte(2), b[27])
		assert.Equal(t, byte(trace.FlagsSampled), b[28])
	})
}

func TestSpanContextFromBinary(t *testing.T) {
	t.Run("round trips through BinaryFromSpanContext", func(t *testing.T) {
		traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
		assert.NoError(t, err)
		spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
		assert.NoError(t, err)
		want := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		})

		got, ok := SpanContextFromBinary(BinaryFromSpanContext(want))
		assert.True(t, ok)
		assert.Equal(t, want.TraceID(), got.TraceID())
		assert.Equal(t, want.SpanID(), got.SpanID())
		assert.Equal(t, want.TraceFlags(), got.TraceFlags())
	})

	t.Run("empty bytes", func(t *testing.T) {
		_, ok := SpanContextFromBinary(nil)
		assert.False(t, ok)
	})

	t.Run("unsupported version byte", func(t *testing.T) {
		_, ok := SpanContextFromBinary([]byte{1, 0})
		assert.False(t, ok)
	})

	t.Run("missing trace id section", func(t *testing.T) {
		_, ok := SpanContextFromBinary([]byte{0, 1})
		assert.False(t, ok)
	})

	t.Run("trace id section too short", func(t *testing.T) {
		_, ok := SpanContextFromBinary([]byte{0, 0, 1, 2, 3})
		assert.False(t, ok)
	})

	t.Run("valid trace id, no span id or flags", func(t *testing.T) {
		traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
		assert.NoError(t, err)
		b := make([]byte, 0, 18)
		b = append(b, 0, 0)
		b = append(b, traceID[:]...)

		sc, ok := SpanContextFromBinary(b)
		assert.True(t, ok)
		assert.Equal(t, traceID, sc.TraceID())
		assert.False(t, sc.SpanID().IsValid())
	})
}
