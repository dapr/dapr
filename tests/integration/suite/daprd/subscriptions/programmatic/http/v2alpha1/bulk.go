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

package v2alpha1

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/api/http"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/stretchr/testify/assert"

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
		subscriber.WithProgrammaticSubscriptions(subscriber.SubscriptionJSON{
			PubsubName: "mypub",
			Topic:      "a",
			Routes: subscriber.RoutesJSON{
				Default: "/a",
			},
			BulkSubscribe: subscriber.BulkSubscribeJSON{
				Enabled:            true,
				MaxMessagesCount:   100,
				MaxAwaitDurationMs: 40,
			},
		},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "b",
				Routes: subscriber.RoutesJSON{
					Default: "/b",
				},
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

	var subsInMeta []http.MetadataResponsePubsubSubscription
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		subsInMeta = b.daprd.GetMetaSubscriptions(c, ctx)
		assert.Len(c, subsInMeta, 2)
	}, time.Second*2, time.Millisecond*10)
	assert.ElementsMatch(t, []http.MetadataResponsePubsubSubscription{
		{PubsubName: "mypub", Topic: "a", Rules: []http.MetadataResponsePubsubSubscriptionRule{{Path: "/a"}}, Type: rtv1.PubsubSubscriptionType_PROGRAMMATIC},
		{PubsubName: "mypub", Topic: "b", Rules: []http.MetadataResponsePubsubSubscriptionRule{{Path: "/b"}}, Type: rtv1.PubsubSubscriptionType_PROGRAMMATIC},
	},
		subsInMeta,
	)
}
