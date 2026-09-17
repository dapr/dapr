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

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
	grpcMetadata "google.golang.org/grpc/metadata"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
	testtrace "github.com/dapr/dapr/pkg/testing/trace"
)

const tracingTestTraceParent = "00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-01"

// bulkTracingTopic is unique to this test so that spans started by tests running
// in parallel, which share the process-wide recorder, can be told apart.
const bulkTracingTopic = "bulk-tracing-topic"

func TestDeliverBulkTracing(t *testing.T) {
	// The global tracer provider takes a delegate once per process, so one
	// recorder serves the whole package.
	recorder := testtrace.NewSpanRecorder()

	newRequest := func(cloudEvents ...map[string]any) *postman.DeliverBulkRequest {
		messages := make([]todo.Message, len(cloudEvents))
		entryIDIndexMap := make(map[string]int, len(cloudEvents))

		for i, cloudEvent := range cloudEvents {
			entryID, _ := cloudEvent[contribpubsub.IDField].(string)
			entry := contribpubsub.BulkMessageEntry{EntryId: entryID, Event: []byte("data"), ContentType: "text/plain"}

			messages[i] = todo.Message{
				CloudEvent: cloudEvent,
				RawData:    &pubsub.BulkSubscribeMessageItem{EntryId: entryID},
				Entry:      &entry,
			}
			entryIDIndexMap[entryID] = i
		}

		bulkResponses := make([]contribpubsub.BulkSubscribeResponseEntry, 0, len(cloudEvents))
		bulkSubDiag := todo.NewBulkSubIngressDiagnostics()

		return &postman.DeliverBulkRequest{
			BulkSubCallData: &todo.BulkSubscribeCallData{
				BulkResponses:   &bulkResponses,
				BulkSubDiag:     &bulkSubDiag,
				EntryIdIndexMap: &entryIDIndexMap,
				PsName:          "testpubsub",
				Topic:           bulkTracingTopic,
			},
			BulkSubMsg: &todo.BulkSubscribedMessage{
				PubSubMessages: messages,
				Topic:          bulkTracingTopic,
				Pubsub:         "testpubsub",
				Path:           bulkTracingTopic,
				Length:         len(messages),
			},
			BulkSubResiliencyRes: &todo.BulkSubscribeResiliencyRes{
				Entries:  make([]contribpubsub.BulkSubscribeResponseEntry, 0),
				Envelope: map[string]any{},
			},
			BulkResponses: &bulkResponses,
		}
	}

	// newPostman returns a postman whose app client records the context of the
	// bulk call, and answers every entry with a success.
	newPostman := func(tracing *config.TracingSpec) (postman.Interface, *context.Context) {
		var delivered context.Context

		mockClientConn := channelt.MockClientConn{
			InvokeFn: func(ctx context.Context, method string, args any, reply any, opts ...googlegrpc.CallOption) error {
				delivered = ctx

				req, ok := args.(*rtv1.TopicEventBulkRequest)
				require.True(t, ok)

				res, ok := reply.(*rtv1.TopicEventBulkResponse)
				require.True(t, ok)

				for _, entry := range req.GetEntries() {
					res.Statuses = append(res.GetStatuses(), &rtv1.TopicEventBulkResponseEntry{
						EntryId: entry.GetEntryId(),
						Status:  rtv1.TopicEventResponse_SUCCESS,
					})
				}

				return nil
			},
		}

		channel := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{})
		channel.SetAppClientConn(&mockClientConn)

		return New(Options{Channel: channel, Tracing: tracing}), &delivered
	}

	// startedBulkSpans returns only the spans this test started, so spans from
	// tests running in parallel are ignored.
	startedBulkSpans := func() []testtrace.StartedSpan {
		var spans []testtrace.StartedSpan

		for _, span := range recorder.Started() {
			if span.Name == "pubsub/"+bulkTracingTopic {
				spans = append(spans, span)
			}
		}

		return spans
	}

	// deliveredTraceParents returns every traceparent the app was handed. An
	// empty result is the symptom this fix addresses: the app is given no trace
	// context to continue from.
	deliveredTraceParents := func(ctx context.Context) []string {
		md, ok := grpcMetadata.FromOutgoingContext(ctx)
		if !ok {
			return nil
		}

		return md.Get(contribpubsub.TraceParentField)
	}

	t.Run("every entry is traced, with or without inbound trace context", func(t *testing.T) {
		recorder.Reset()

		g, delivered := newPostman(&config.TracingSpec{SamplingRate: "1"})

		req := newRequest(
			map[string]any{contribpubsub.IDField: "1"},
			map[string]any{contribpubsub.IDField: "2", contribpubsub.TraceParentField: tracingTestTraceParent},
			map[string]any{contribpubsub.IDField: "3", contribpubsub.TraceParentField: "not-a-traceparent"},
			map[string]any{contribpubsub.IDField: "4", contribpubsub.TraceParentField: float64(12345)},
		)

		require.NoError(t, g.DeliverBulk(t.Context(), req))

		// One span per entry, not one per entry that happened to carry a
		// traceparent. Before the fix only entry 2 was traced.
		spans := startedBulkSpans()
		require.Len(t, spans, 4)

		parent, ok := diag.SpanContextFromW3CString(tracingTestTraceParent)
		require.True(t, ok)

		var continued, roots int

		for _, span := range spans {
			switch {
			case span.Parent.SpanID() == parent.SpanID():
				continued++
			case !span.Parent.IsValid():
				roots++
			}
		}

		assert.Equal(t, 1, continued, "the entry carrying a traceparent continues its trace")
		assert.Equal(t, 3, roots, "the entries with no usable trace context each start a new root span")

		traceParents := deliveredTraceParents(*delivered)
		require.Len(t, traceParents, 4, "the app must be given a trace context for every entry")

		for _, traceParent := range traceParents {
			_, ok := diag.SpanContextFromW3CString(traceParent)
			assert.True(t, ok, "injected traceparent %q must be parseable", traceParent)
		}
	})

	t.Run("tracing disabled starts no span", func(t *testing.T) {
		recorder.Reset()

		g, delivered := newPostman(&config.TracingSpec{SamplingRate: "0"})

		req := newRequest(
			map[string]any{contribpubsub.IDField: "1"},
			map[string]any{contribpubsub.IDField: "2", contribpubsub.TraceParentField: tracingTestTraceParent},
		)

		require.NoError(t, g.DeliverBulk(t.Context(), req))

		assert.Empty(t, startedBulkSpans())
		assert.Empty(t, deliveredTraceParents(*delivered))
	})
}
