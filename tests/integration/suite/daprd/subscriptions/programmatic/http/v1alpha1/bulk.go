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

package v1alpha1

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bulk))
}

type bulk struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (b *bulk) Setup(t *testing.T) []framework.Option {
	b.sub = subscriber.New(t,
		subscriber.WithBulkRoutes("/a", "/b"),
		subscriber.WithProgrammaticSubscriptions(
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "a",
				Route:      "/a",
				BulkSubscribe: subscriber.BulkSubscribeJSON{
					Enabled:            true,
					MaxMessagesCount:   100,
					MaxAwaitDurationMs: 40,
				},
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "b",
				Route:      "/b",
			},
		),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.sub.Port()),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
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

func (b *bulk) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	// TODO: @joshvanl: add support for bulk publish to in-memory pubsub.
	b.sub.PublishBulk(t, ctx, subscriber.PublishBulkRequest{
		Daprd:      b.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Entries: []subscriber.PublishBulkRequestEntry{
			{EntryID: "1", Event: `{"id": 1}`, ContentType: "application/json"},
			{EntryID: "2", Event: `{"id": 2}`, ContentType: "application/json"},
			{EntryID: "3", Event: `{"id": 3}`, ContentType: "application/json"},
			{EntryID: "4", Event: `{"id": 4}`, ContentType: "application/json"},
		},
	})

	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)

	b.sub.PublishBulk(t, ctx, subscriber.PublishBulkRequest{
		Daprd:      b.daprd,
		PubSubName: "mypub",
		Topic:      "b",
		Entries: []subscriber.PublishBulkRequestEntry{
			{EntryID: "1", Event: `{"id": 1}`, ContentType: "application/json"},
			{EntryID: "2", Event: `{"id": 2}`, ContentType: "application/json"},
			{EntryID: "3", Event: `{"id": 3}`, ContentType: "application/json"},
			{EntryID: "4", Event: `{"id": 4}`, ContentType: "application/json"},
		},
	})

	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
	b.sub.ReceiveBulk(t, ctx)
}
