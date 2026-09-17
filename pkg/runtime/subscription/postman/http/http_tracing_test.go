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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
)

const tracingTestTraceParent = "00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-01"

// The global tracer provider takes a delegate once per process, so the whole
// package shares a single recorder.
var spanRecorder = sync.OnceValue(testtrace.NewSpanRecorder)

var (
	tracingOn  = &config.TracingSpec{SamplingRate: "1"}
	tracingOff = &config.TracingSpec{SamplingRate: "0"}
)

// deliveredSpanContext is the span context the postman hands to the app
// channel. An invalid span context means the delivery was not traced, which is
// what the app sees as an empty traceparent header.
func deliveredSpanContext(ctx context.Context) trace.SpanContext {
	return trace.SpanFromContext(ctx).SpanContext()
}

func okResponse(t *testing.T) *invokev1.InvokeMethodResponse {
	t.Helper()

	var (
		appResp contribpubsub.AppResponse
		buf     bytes.Buffer
	)

	require.NoError(t, json.NewEncoder(&buf).Encode(appResp))

	return invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf).WithContentType("application/json")
}

// newTracingAppChannel returns an app channel that answers every invocation with
// a success, and a pointer to the context it was last invoked with.
func newTracingAppChannel(t *testing.T, resp *invokev1.InvokeMethodResponse) (*channelt.MockAppChannel, *context.Context) {
	t.Helper()

	mockAppChannel := new(channelt.MockAppChannel)

	var delivered context.Context

	mockAppChannel.
		On("InvokeMethod", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			delivered, _ = args.Get(0).(context.Context)
		}).
		Return(resp, nil)

	return mockAppChannel, &delivered
}

func TestDeliverTracing(t *testing.T) {
	recorder := spanRecorder()

	newMessage := func(cloudEvent map[string]any) *runtimePubsub.SubscribedMessage {
		return &runtimePubsub.SubscribedMessage{
			CloudEvent: cloudEvent,
			Topic:      "topic1",
			Data:       []byte("testing"),
			Metadata:   map[string]string{"pubsubName": "testpubsub"},
			Path:       "topic1",
			PubSub:     "testpubsub",
		}
	}

	t.Run("a cloud event with no trace context is delivered under a new root span", func(t *testing.T) {
		recorder.Reset()

		resp := okResponse(t)
		defer resp.Close()

		mockAppChannel, delivered := newTracingAppChannel(t, resp)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
			Tracing:  tracingOn,
		})

		require.NoError(t, h.Deliver(t.Context(), newMessage(map[string]any{contribpubsub.IDField: "1"})))
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)

		sc := deliveredSpanContext(*delivered)
		require.True(t, sc.IsValid(), "the app must be given a usable trace context, not an empty one")

		recorded, ok := recorder.BySpanID(sc.SpanID())
		require.True(t, ok)
		assert.Equal(t, "pubsub/topic1", recorded.Name)
		assert.False(t, recorded.Parent.IsValid(), "a message with no inbound trace context must start a new root span")
	})

	t.Run("a cloud event with a traceparent continues the inbound trace", func(t *testing.T) {
		recorder.Reset()

		resp := okResponse(t)
		defer resp.Close()

		mockAppChannel, delivered := newTracingAppChannel(t, resp)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
			Tracing:  tracingOn,
		})

		require.NoError(t, h.Deliver(t.Context(), newMessage(map[string]any{
			contribpubsub.IDField:          "1",
			contribpubsub.TraceParentField: tracingTestTraceParent,
		})))

		parent, ok := diag.SpanContextFromW3CString(tracingTestTraceParent)
		require.True(t, ok)

		sc := deliveredSpanContext(*delivered)
		require.True(t, sc.IsValid())
		assert.Equal(t, parent.TraceID(), sc.TraceID())

		recorded, ok := recorder.BySpanID(sc.SpanID())
		require.True(t, ok)
		assert.Equal(t, parent.SpanID(), recorded.Parent.SpanID())
	})

	t.Run("a non-string trace context is ignored and a new root span started", func(t *testing.T) {
		recorder.Reset()

		resp := okResponse(t)
		defer resp.Close()

		mockAppChannel, delivered := newTracingAppChannel(t, resp)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
			Tracing:  tracingOn,
		})

		require.NoError(t, h.Deliver(t.Context(), newMessage(map[string]any{
			contribpubsub.IDField:          "1",
			contribpubsub.TraceParentField: float64(12345),
		})))

		sc := deliveredSpanContext(*delivered)
		require.True(t, sc.IsValid())

		recorded, ok := recorder.BySpanID(sc.SpanID())
		require.True(t, ok)
		assert.False(t, recorded.Parent.IsValid())
	})

	t.Run("tracing disabled starts no span", func(t *testing.T) {
		recorder.Reset()

		resp := okResponse(t)
		defer resp.Close()

		mockAppChannel, delivered := newTracingAppChannel(t, resp)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
			Tracing:  tracingOff,
		})

		require.NoError(t, h.Deliver(t.Context(), newMessage(map[string]any{contribpubsub.IDField: "1"})))

		assert.False(t, deliveredSpanContext(*delivered).IsValid())
		assert.Empty(t, recorder.Names())
	})
}

func TestDeliverBulkTracing(t *testing.T) {
	recorder := spanRecorder()

	// newBulkRequest builds a bulk delivery of one entry per cloud event given.
	newBulkRequest := func(cloudEvents ...map[string]any) (*postman.DeliverBulkRequest, *invokev1.InvokeMethodResponse) {
		messages := make([]todo.Message, len(cloudEvents))
		appResponses := make([]contribpubsub.AppBulkResponseEntry, len(cloudEvents))
		entryIDIndexMap := make(map[string]int, len(cloudEvents))

		for i, cloudEvent := range cloudEvents {
			entryID := "entry-" + cloudEvent[contribpubsub.IDField].(string)
			entry := contribpubsub.BulkMessageEntry{EntryId: entryID, Event: []byte("data"), ContentType: "text/plain"}

			messages[i] = todo.Message{
				CloudEvent: cloudEvent,
				RawData:    &runtimePubsub.BulkSubscribeMessageItem{EntryId: entryID},
				Entry:      &entry,
			}
			appResponses[i] = contribpubsub.AppBulkResponseEntry{EntryId: entryID, Status: contribpubsub.Success}
			entryIDIndexMap[entryID] = i
		}

		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(contribpubsub.AppBulkResponse{AppResponses: appResponses}))
		resp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf).WithContentType("application/json")

		bulkResponses := make([]contribpubsub.BulkSubscribeResponseEntry, len(cloudEvents))
		bulkSubDiag := todo.NewBulkSubIngressDiagnostics()

		return &postman.DeliverBulkRequest{
			BulkSubCallData: &todo.BulkSubscribeCallData{
				BulkResponses:   &bulkResponses,
				BulkSubDiag:     &bulkSubDiag,
				EntryIdIndexMap: &entryIDIndexMap,
				PsName:          "testpubsub",
				Topic:           "topic1",
			},
			BulkSubMsg: &todo.BulkSubscribedMessage{
				PubSubMessages: messages,
				Topic:          "topic1",
				Pubsub:         "testpubsub",
				Path:           "topic1",
				Length:         len(messages),
			},
			BulkSubResiliencyRes: &todo.BulkSubscribeResiliencyRes{
				Entries:  make([]contribpubsub.BulkSubscribeResponseEntry, 0),
				Envelope: map[string]any{},
			},
		}, resp
	}

	t.Run("every entry is traced, with or without inbound trace context", func(t *testing.T) {
		recorder.Reset()

		req, resp := newBulkRequest(
			map[string]any{contribpubsub.IDField: "1"},
			map[string]any{contribpubsub.IDField: "2", contribpubsub.TraceParentField: tracingTestTraceParent},
			map[string]any{contribpubsub.IDField: "3", contribpubsub.TraceParentField: "not-a-traceparent"},
		)
		defer resp.Close()

		mockAppChannel, delivered := newTracingAppChannel(t, resp)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
			Tracing:  tracingOn,
		})

		require.NoError(t, h.DeliverBulk(t.Context(), req))
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)

		// One span per entry, not one per entry that happened to carry a
		// traceparent. Before the fix only entry 2 was traced.
		assert.Equal(t, []string{"pubsub/topic1", "pubsub/topic1", "pubsub/topic1"}, recorder.Names())

		parent, ok := diag.SpanContextFromW3CString(tracingTestTraceParent)
		require.True(t, ok)

		var continued, roots int

		for _, span := range recorder.Started() {
			if span.Parent.SpanID() == parent.SpanID() {
				continued++
			} else if !span.Parent.IsValid() {
				roots++
			}
		}

		assert.Equal(t, 1, continued, "the entry carrying a traceparent continues its trace")
		assert.Equal(t, 2, roots, "the entries with no usable trace context each start a new root span")

		assert.True(t, deliveredSpanContext(*delivered).IsValid())
	})

	t.Run("tracing disabled starts no span", func(t *testing.T) {
		recorder.Reset()

		req, resp := newBulkRequest(map[string]any{contribpubsub.IDField: "1"})
		defer resp.Close()

		mockAppChannel, delivered := newTracingAppChannel(t, resp)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
			Tracing:  tracingOff,
		})

		require.NoError(t, h.DeliverBulk(t.Context(), req))

		assert.Empty(t, recorder.Names())
		assert.False(t, deliveredSpanContext(*delivered).IsValid())
	})
}

// TestDeliverEndsSpanOnAppChannelError covers the delivery span being ended on
// the error path as well as the success path. The span is started before the
// app is called, so a path that returns early leaks it: the span never reaches
// an exporter and the trace is left open. Starting a span for every message
// makes that leak reachable on every failed delivery, not only on the ones that
// happened to carry a traceparent.
func TestDeliverEndsSpanOnAppChannelError(t *testing.T) {
	recorder := spanRecorder()
	recorder.Reset()

	mockAppChannel := new(channelt.MockAppChannel)
	mockAppChannel.
		On("InvokeMethod", mock.Anything, mock.Anything).
		Return(nil, assert.AnError)

	h := New(Options{
		Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		Tracing:  tracingOn,
	})

	msg := &runtimePubsub.SubscribedMessage{
		CloudEvent: map[string]any{contribpubsub.IDField: "1"},
		Topic:      "topic1",
		Data:       []byte("testing"),
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
		PubSub:     "testpubsub",
	}

	require.Error(t, h.Deliver(t.Context(), msg))

	started := recorder.Started()
	require.Len(t, started, 1, "the delivery must still be traced when the app call fails")

	assert.True(t, recorder.Ended(started[0].SpanContext.SpanID()),
		"a delivery that returns early must not leave its span unended")
}
