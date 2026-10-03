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

package traceparent

import (
	"context"
	"encoding/hex"
	"fmt"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rootspan))
}

// rootspan is the gRPC counterpart of the HTTP root span case: a subscribed
// message whose cloud event carries no trace context must still be delivered
// under a new root span, with a usable traceparent in the gRPC metadata rather
// than none at all.
type rootspan struct {
	daprd     *daprd.Daprd
	collector *otel.Collector

	ch chan metadata.MD
}

func (r *rootspan) Setup(t *testing.T) []framework.Option {
	r.ch = make(chan metadata.MD, 1)
	r.collector = otel.New(t)

	app := app.New(t,
		app.WithOnTopicEventFn(func(ctx context.Context, _ *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			r.ch <- md

			return &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS}, nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "raw-topic",
						Routes:     &rtv1.TopicRoutes{Default: "/raw-topic"},
						Metadata:   map[string]string{"rawPayload": "true"},
					},
				},
			}, nil
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		r.collector.GRPCDaprdConfiguration(t),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(r.collector, app, r.daprd),
	}
}

func (r *rootspan) Run(t *testing.T, ctx context.Context) {
	r.collector.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	// A raw payload publish stands in for a publisher outside Dapr:
	// FromRawPayload builds the cloud event on the subscriber side with no
	// trace fields, and a raw publish puts none in the message metadata.
	pubURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/mypub/raw-topic?metadata.rawPayload=true", r.daprd.HTTPPort())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, pubURL, strings.NewReader(`{"message":"hello"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)

	var md metadata.MD

	select {
	case md = <-r.ch:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the pubsub event to be delivered to the app")
	}

	traceparents := md.Get("traceparent")
	require.Len(t, traceparents, 1, "the app must be given a traceparent, not none")

	traceparent := traceparents[0]
	require.Len(t, traceparent, 55, "malformed traceparent %q", traceparent)

	traceID, spanID := traceparent[3:35], traceparent[36:52]
	assert.NotEqual(t, "00000000000000000000000000000000", traceID, "the app must be given a usable trace id")
	assert.NotEqual(t, "0000000000000000", spanID)
	assert.Equal(t, "01", traceparent[53:55], "the delivery span must be sampled at a sampling rate of 1.0")

	// The same span must reach the collector, as a root: the message carried no
	// parent for it to continue from.
	var span *tracepb.Span

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		span = findSpan(r.collector, traceID, spanID)
		assert.NotNil(c, span, "no exported span matched the traceparent the app received")
	}, time.Second*20, time.Millisecond*50)

	assert.Equal(t, "pubsub/raw-topic", span.GetName())
	assert.Empty(t, span.GetParentSpanId(), "a message with no inbound trace context must start a new root span")
}

// findSpan returns the exported span with the given trace and span id, or nil.
func findSpan(collector *otel.Collector, traceID, spanID string) *tracepb.Span {
	for _, resourceSpans := range collector.GetSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				if hex.EncodeToString(span.GetTraceId()) == traceID &&
					hex.EncodeToString(span.GetSpanId()) == spanID {
					return span
				}
			}
		}
	}

	return nil
}
