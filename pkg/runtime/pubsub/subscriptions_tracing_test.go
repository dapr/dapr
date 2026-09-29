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
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	grpcMetadata "google.golang.org/grpc/metadata"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

const (
	testTraceParent     = "00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-01"
	testTracingTopic    = "topic1"
	testTracingContType = "text/plain"
	testTracingData     = "hello"
)

// spanRecorder records the name of every span that is started, whether or not
// that span is ever ended. A span that is started and never ended never reaches
// an exporter, so OnStart is the only place a leaked span can be observed.
type spanRecorder struct {
	lock    sync.Mutex
	started []string
}

func (s *spanRecorder) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.started = append(s.started, span.Name())
}

func (s *spanRecorder) OnEnd(sdktrace.ReadOnlySpan)      {}
func (s *spanRecorder) Shutdown(context.Context) error   { return nil }
func (s *spanRecorder) ForceFlush(context.Context) error { return nil }

func (s *spanRecorder) reset() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.started = nil
}

func (s *spanRecorder) names() []string {
	s.lock.Lock()
	defer s.lock.Unlock()
	return slices.Clone(s.started)
}

func outgoingTraceParent(t *testing.T, ctx context.Context) string {
	t.Helper()

	md, ok := grpcMetadata.FromOutgoingContext(ctx)
	require.True(t, ok, "expected outgoing gRPC metadata on the returned context")

	values := md.Get(contribpubsub.TraceParentField)
	require.Len(t, values, 1)

	return values[0]
}

func TestGRPCEnvelopeFromSubscriptionMessage(t *testing.T) {
	recorder := new(spanRecorder)
	// The diagnostics package resolves its tracer from the global provider the
	// first time a delegate is set, so the provider is installed once for the
	// whole test rather than per subtest.
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

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
		recorder.reset()

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(newCloudEvent()), log, tracingOn)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		require.NotNil(t, span)
		defer span.End()

		assert.Equal(t, []string{"pubsub/" + testTracingTopic}, recorder.names())
		assert.True(t, span.SpanContext().IsValid())

		injected, ok := diag.SpanContextFromW3CString(outgoingTraceParent(t, ctx))
		require.True(t, ok, "the injected traceparent must be parseable")
		assert.Equal(t, span.SpanContext().TraceID(), injected.TraceID())
		assert.Equal(t, span.SpanContext().SpanID(), injected.SpanID())
	})

	t.Run("cloud event with a traceparent continues the inbound trace", func(t *testing.T) {
		recorder.reset()

		cloudEvent := newCloudEvent()
		cloudEvent[contribpubsub.TraceParentField] = testTraceParent

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(cloudEvent), log, tracingOn)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		require.NotNil(t, span)
		defer span.End()

		assert.Equal(t, []string{"pubsub/" + testTracingTopic}, recorder.names())

		parent, ok := diag.SpanContextFromW3CString(testTraceParent)
		require.True(t, ok)
		assert.Equal(t, parent.TraceID(), span.SpanContext().TraceID())
		assert.NotEqual(t, parent.SpanID(), span.SpanContext().SpanID())

		injected, ok := diag.SpanContextFromW3CString(outgoingTraceParent(t, ctx))
		require.True(t, ok)
		assert.Equal(t, parent.TraceID(), injected.TraceID())
	})

	t.Run("cloud event with an unparseable traceparent still starts a span", func(t *testing.T) {
		recorder.reset()

		cloudEvent := newCloudEvent()
		cloudEvent[contribpubsub.TraceParentField] = "not-a-traceparent"

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(cloudEvent), log, tracingOn)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		require.NotNil(t, span)
		defer span.End()

		assert.Equal(t, []string{"pubsub/" + testTracingTopic}, recorder.names())
		assert.True(t, span.SpanContext().IsValid())

		injected, ok := diag.SpanContextFromW3CString(outgoingTraceParent(t, ctx))
		require.True(t, ok)
		assert.Equal(t, span.SpanContext().TraceID(), injected.TraceID())
	})

	t.Run("tracing disabled returns no span and injects nothing", func(t *testing.T) {
		recorder.reset()

		ctx, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(newCloudEvent()), log, tracingOff)
		require.NoError(t, err)
		require.NotNil(t, envelope)
		assert.Nil(t, span)
		assert.Empty(t, recorder.names())

		_, ok := grpcMetadata.FromOutgoingContext(ctx)
		assert.False(t, ok, "no trace metadata should be added when tracing is disabled")
	})

	t.Run("failing to extract extensions leaves no unended span behind", func(t *testing.T) {
		recorder.reset()

		cloudEvent := newCloudEvent()
		// Channels cannot be marshalled to JSON, so extension extraction fails.
		cloudEvent["badExtension"] = make(chan int)

		_, envelope, span, err := GRPCEnvelopeFromSubscriptionMessage(t.Context(), newMessage(cloudEvent), log, tracingOn)
		require.Error(t, err)
		assert.Nil(t, envelope)
		assert.Nil(t, span)
		assert.Empty(t, recorder.names(), "a path that returns an error must not start a span the caller cannot end")
	})
}
