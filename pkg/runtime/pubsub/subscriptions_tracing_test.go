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

package pubsub

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcMetadata "google.golang.org/grpc/metadata"

	"go.opentelemetry.io/otel/trace"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
)

const (
	testTraceParent     = "00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-01"
	testTracingTopic    = "topic1"
	testTracingContType = "text/plain"
	testTracingData     = "hello"
)

func outgoingTraceParent(t *testing.T, ctx context.Context) string {
	t.Helper()

	md, ok := grpcMetadata.FromOutgoingContext(ctx)
	require.True(t, ok, "expected outgoing gRPC metadata on the returned context")

	values := md.Get(contribpubsub.TraceParentField)
	require.Len(t, values, 1)

	return values[0]
}

func TestGRPCEnvelopeFromSubscriptionMessage(t *testing.T) {
	// The diagnostics package resolves its tracer from the global provider the
	// first time a delegate is set, so the recorder is installed once for the
	// whole test rather than per subtest.
	recorder := testtrace.NewSpanRecorder()

	tracingOn := &config.TracingSpec{SamplingRate: "1"}
	tracingOff := &config.TracingSpec{SamplingRate: "0"}

	newCloudEvent := func() map[string]any {
		return map[string]any{
			contribpubsub.IDField:              "id-1",
			contribpubsub.SourceField:          "app1",
			contribpubsub.SpecVersionField:     "1.0",
			contribpubsub.TypeField:            contribpubsub.DefaultCloudEventType,
			contribpubsub.DataContentTypeField: testTracingContType,
			contribpubsub.DataField:            testTracingData,
		}
	}

	newMessage := func(cloudEvent map[string]any) *SubscribedMessage {
		return &SubscribedMessage{
			CloudEvent: cloudEvent,
			Topic:      testTracingTopic,
			Metadata:   map[string]string{MetadataKeyPubSub: "testpubsub"},
			Path:       testTracingTopic,
			PubSub:     "testpubsub",
		}
	}

	t.Run("cloud event without trace context starts a new root span and injects a traceparent", func(t *testing.T) {
		recorder.Reset()

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(newCloudEvent()), log, tracingOn)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		require.NotNil(t, span)
		defer span.End()

		assert.Equal(t, []string{"pubsub/" + testTracingTopic}, recorder.Names())
		assert.True(t, span.SpanContext().IsValid())

		recorded, ok := recorder.BySpanID(span.SpanContext().SpanID())
		require.True(t, ok)
		assert.False(t, recorded.Parent.IsValid(), "a message with no inbound trace context must start a new root span")

		injected, ok := diag.SpanContextFromW3CString(outgoingTraceParent(t, ctx))
		require.True(t, ok, "the injected traceparent must be parseable")
		assert.Equal(t, span.SpanContext().TraceID(), injected.TraceID())
		assert.Equal(t, span.SpanContext().SpanID(), injected.SpanID())
	})

	t.Run("cloud event with a traceparent continues the inbound trace", func(t *testing.T) {
		recorder.Reset()

		cloudEvent := newCloudEvent()
		cloudEvent[contribpubsub.TraceParentField] = testTraceParent

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(cloudEvent), log, tracingOn)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		require.NotNil(t, span)
		defer span.End()

		assert.Equal(t, []string{"pubsub/" + testTracingTopic}, recorder.Names())

		parent, ok := diag.SpanContextFromW3CString(testTraceParent)
		require.True(t, ok)
		assert.Equal(t, parent.TraceID(), span.SpanContext().TraceID())
		assert.NotEqual(t, parent.SpanID(), span.SpanContext().SpanID())

		recorded, ok := recorder.BySpanID(span.SpanContext().SpanID())
		require.True(t, ok)
		assert.Equal(t, parent.SpanID(), recorded.Parent.SpanID(), "the inbound traceparent must be the span's parent")

		injected, ok := diag.SpanContextFromW3CString(outgoingTraceParent(t, ctx))
		require.True(t, ok)
		assert.Equal(t, parent.TraceID(), injected.TraceID())
	})

	t.Run("cloud event with an unparseable traceparent still starts a span", func(t *testing.T) {
		recorder.Reset()

		cloudEvent := newCloudEvent()
		cloudEvent[contribpubsub.TraceParentField] = "not-a-traceparent"

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(cloudEvent), log, tracingOn)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		require.NotNil(t, span)
		defer span.End()

		assert.Equal(t, []string{"pubsub/" + testTracingTopic}, recorder.Names())
		assert.True(t, span.SpanContext().IsValid())

		recorded, ok := recorder.BySpanID(span.SpanContext().SpanID())
		require.True(t, ok)
		assert.False(t, recorded.Parent.IsValid(), "an unusable traceparent must not be adopted as a parent")

		injected, ok := diag.SpanContextFromW3CString(outgoingTraceParent(t, ctx))
		require.True(t, ok)
		assert.Equal(t, span.SpanContext().TraceID(), injected.TraceID())
	})

	t.Run("tracing disabled returns no span and injects nothing", func(t *testing.T) {
		recorder.Reset()

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(newCloudEvent()), log, tracingOff)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		assert.Nil(t, span)
		assert.Empty(t, recorder.Names())

		_, ok := grpcMetadata.FromOutgoingContext(ctx)
		assert.False(t, ok, "no trace metadata should be added when tracing is disabled")
	})

	t.Run("failing to extract extensions leaves no unended span behind", func(t *testing.T) {
		recorder.Reset()

		cloudEvent := newCloudEvent()
		// Channels cannot be marshalled to JSON, so extension extraction fails.
		cloudEvent["badExtension"] = make(chan int)

		_, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(cloudEvent), log, tracingOn)
		require.Error(t, err)
		assert.Nil(t, envelope)
		assert.Nil(t, span)
		assert.Empty(t, recorder.Names(), "a path that returns an error must not start a span the caller cannot end")
	})
}

func TestParentSpanContextFromCloudEvent(t *testing.T) {
	parent, ok := diag.SpanContextFromW3CString(testTraceParent)
	require.True(t, ok)

	tests := map[string]struct {
		cloudEvent map[string]any
		expect     trace.SpanContext
	}{
		"no trace context at all": {
			cloudEvent: map[string]any{contribpubsub.IDField: "1"},
			expect:     trace.SpanContext{},
		},
		"traceparent": {
			cloudEvent: map[string]any{contribpubsub.TraceParentField: testTraceParent},
			expect:     parent,
		},
		"legacy traceid only": {
			cloudEvent: map[string]any{contribpubsub.TraceIDField: testTraceParent},
			expect:     parent,
		},
		"traceparent wins over the legacy traceid": {
			cloudEvent: map[string]any{
				contribpubsub.TraceParentField: testTraceParent,
				contribpubsub.TraceIDField:     "00-00000000000000000000000000000001-0000000000000001-01",
			},
			expect: parent,
		},
		"unparseable traceparent": {
			cloudEvent: map[string]any{contribpubsub.TraceParentField: "not-a-traceparent"},
			expect:     trace.SpanContext{},
		},
		"empty traceparent": {
			cloudEvent: map[string]any{contribpubsub.TraceParentField: ""},
			expect:     trace.SpanContext{},
		},
		"non-string traceparent": {
			cloudEvent: map[string]any{contribpubsub.TraceParentField: float64(12345)},
			expect:     trace.SpanContext{},
		},
		"nil cloud event": {
			cloudEvent: nil,
			expect:     trace.SpanContext{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			sc := ParentSpanContextFromCloudEvent(test.cloudEvent, log)
			assert.Equal(t, test.expect, sc)
		})
	}
}
