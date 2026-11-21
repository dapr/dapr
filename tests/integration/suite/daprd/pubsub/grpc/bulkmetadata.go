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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bulkmetadata))
}

// bulkmetadata verifies that request-level metadata on a gRPC bulk publish
// request is applied to every entry, including entries that carry no
// metadata of their own, and that entry-level keys still take precedence
// over request-level keys.
type bulkmetadata struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (b *bulkmetadata) Setup(t *testing.T) []framework.Option {
	b.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{{
					PubsubName: "mypub",
					Topic:      "a",
					Routes:     &rtv1.TopicRoutes{Default: "/a"},
				}},
			}, nil
		}),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
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
		framework.WithProcesses(b.sub, b.daprd),
	}
}

func (b *bulkmetadata) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)
	client := b.daprd.GRPCClient(t, ctx)

	// Two signals show whether metadata reached an entry. daprd writes an
	// `expiration` field into the cloud event from `ttlInSeconds` for
	// components without native TTL support, such as the in-memory pub/sub,
	// and `cloudevent.type` overrides the envelope's `type` field.
	// Entry "1" carries no metadata of its own; entry "2" overrides the type.
	before := time.Now().UTC()
	resp, err := client.BulkPublishEvent(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "mypub",
		Topic:      "a",
		Metadata: map[string]string{
			"ttlInSeconds":    "300",
			"cloudevent.type": "request-type",
		},
		Entries: []*rtv1.BulkPublishRequestEntry{
			{
				EntryId:     "1",
				Event:       []byte(`{"id":1}`),
				ContentType: "application/json",
			},
			{
				EntryId:     "2",
				Event:       []byte(`{"id":2}`),
				ContentType: "application/json",
				Metadata:    map[string]string{"cloudevent.type": "entry-type"},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetFailedEntries())

	events := make(map[string]*rtv1.TopicEventRequest, 2)
	for range 2 {
		in := b.sub.Receive(t, ctx)
		events[string(in.GetData())] = in
	}
	b.sub.AssertEventChanLen(t, 0)

	assertExpiration := func(t *testing.T, in *rtv1.TopicEventRequest) {
		t.Helper()
		field, ok := in.GetExtensions().GetFields()["expiration"]
		require.True(t, ok, "expiration missing from cloud event extensions: %v", in.GetExtensions())
		expiration, err := time.Parse(time.RFC3339, field.GetStringValue())
		require.NoError(t, err)
		assert.WithinRange(t, expiration, before.Add(300*time.Second).Truncate(time.Second), time.Now().UTC().Add(300*time.Second))
	}

	// Entry without its own metadata inherits the request-level metadata.
	require.Contains(t, events, `{"id":1}`)
	assert.Equal(t, "request-type", events[`{"id":1}`].GetType())
	assertExpiration(t, events[`{"id":1}`])

	// Entry with its own metadata still inherits request-level keys, but its
	// own keys take precedence.
	require.Contains(t, events, `{"id":2}`)
	assert.Equal(t, "entry-type", events[`{"id":2}`].GetType())
	assertExpiration(t, events[`{"id":2}`])
}
