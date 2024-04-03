/*
Copyright 2024 The Dapr Authors
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

package bulk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.sub = subscriber.New(t)

	g.daprd = daprd.New(t,
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mypub
spec:
 type: pubsub.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: mysub
spec:
 pubsubname: mypub
 topic: a
 route: /a
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 100
  maxAwaitDurationMs: 40
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: nobulk
spec:
 pubsubname: mypub
 topic: b
 route: /b
`))

	return []framework.Option{
		framework.WithProcesses(g.sub, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)

	// TODO: @joshvanl: add support for bulk publish to in-memory pubsub.
	resp, err := client.BulkPublishEventAlpha1(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "mypub",
		Topic:      "a",
		Entries: []*rtv1.BulkPublishRequestEntry{
			{EntryId: "1", Event: []byte(`{"id": 1}`), ContentType: "application/json"},
			{EntryId: "2", Event: []byte(`{"id": 2}`), ContentType: "application/json"},
			{EntryId: "3", Event: []byte(`{"id": 3}`), ContentType: "application/json"},
			{EntryId: "4", Event: []byte(`{"id": 4}`), ContentType: "application/json"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, len(resp.GetFailedEntries()))
	g.sub.ReceiveBulk(t, ctx)
	g.sub.ReceiveBulk(t, ctx)
	g.sub.ReceiveBulk(t, ctx)
	g.sub.ReceiveBulk(t, ctx)
	g.sub.AssertBulkEventChanLen(t, 0)

	resp, err = client.BulkPublishEventAlpha1(ctx, &rtv1.BulkPublishRequest{
		PubsubName: "mypub",
		Topic:      "b",
		Entries: []*rtv1.BulkPublishRequestEntry{
			{EntryId: "1", Event: []byte(`{"id": 1}`), ContentType: "application/json"},
			{EntryId: "2", Event: []byte(`{"id": 2}`), ContentType: "application/json"},
			{EntryId: "3", Event: []byte(`{"id": 3}`), ContentType: "application/json"},
			{EntryId: "4", Event: []byte(`{"id": 4}`), ContentType: "application/json"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, len(resp.GetFailedEntries()))
	g.sub.AssertBulkEventChanLen(t, 0)
}
