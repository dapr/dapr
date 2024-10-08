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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t, subscriber.WithBulkRoutes("/a", "/b"))

	h.daprd = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
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
		framework.WithProcesses(h.sub, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	// TODO: @joshvanl: add support for bulk publish to in-memory pubsub.
	h.sub.PublishBulk(t, ctx, subscriber.PublishBulkRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Entries: []subscriber.PublishBulkRequestEntry{
			{EntryID: "1", Event: `{"id": 1}`, ContentType: "application/json"},
			{EntryID: "2", Event: `{"id": 2}`, ContentType: "application/json"},
			{EntryID: "3", Event: `{"id": 3}`, ContentType: "application/json"},
			{EntryID: "4", Event: `{"id": 4}`, ContentType: "application/json"},
		},
	})

	h.sub.ReceiveBulk(t, ctx)
	h.sub.ReceiveBulk(t, ctx)
	h.sub.ReceiveBulk(t, ctx)
	h.sub.ReceiveBulk(t, ctx)

	h.sub.PublishBulk(t, ctx, subscriber.PublishBulkRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "b",
		Entries: []subscriber.PublishBulkRequestEntry{
			{EntryID: "1", Event: `{"id": 1}`, ContentType: "application/json"},
			{EntryID: "2", Event: `{"id": 2}`, ContentType: "application/json"},
			{EntryID: "3", Event: `{"id": 3}`, ContentType: "application/json"},
			{EntryID: "4", Event: `{"id": 4}`, ContentType: "application/json"},
		},
	})

	h.sub.ReceiveBulk(t, ctx)
	h.sub.ReceiveBulk(t, ctx)
	h.sub.ReceiveBulk(t, ctx)
	h.sub.ReceiveBulk(t, ctx)
}
