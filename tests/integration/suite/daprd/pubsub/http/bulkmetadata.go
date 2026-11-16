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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bulkmetadata))
}

// bulkmetadata verifies that request-level metadata on an HTTP bulk publish
// request is applied to every entry, including entries that carry no
// metadata of their own, and that entry-level keys still take precedence
// over request-level keys.
type bulkmetadata struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (b *bulkmetadata) Setup(t *testing.T) []framework.Option {
	b.sub = subscriber.New(t,
		subscriber.WithRoutes("/a"),
		subscriber.WithProgrammaticSubscriptions(subscriber.SubscriptionJSON{
			PubsubName: "mypub",
			Topic:      "a",
			Route:      "/a",
		}),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.sub.Port()),
		daprd.WithAppProtocol("http"),
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

	// Two signals show whether metadata reached an entry. daprd writes an
	// `expiration` field into the cloud event from `ttlInSeconds` for
	// components without native TTL support, such as the in-memory pub/sub,
	// and `cloudevent.type` overrides the envelope's `type` field.
	// Entry "1" carries no metadata of its own; entry "2" overrides the type.
	before := time.Now().UTC()
	b.sub.PublishBulk(t, ctx, subscriber.PublishBulkRequest{
		Daprd:      b.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Metadata: map[string]string{
			"ttlInSeconds":    "300",
			"cloudevent.type": "request-type",
		},
		Entries: []subscriber.PublishBulkRequestEntry{
			{
				EntryID:     "1",
				Event:       "one",
				ContentType: "text/plain",
			},
			{
				EntryID:     "2",
				Event:       "two",
				ContentType: "text/plain",
				Metadata:    map[string]string{"cloudevent.type": "entry-type"},
			},
		},
	})

	events := make(map[string]*subscriber.RouteEvent, 2)
	for range 2 {
		ev := b.sub.Receive(t, ctx)
		events[string(ev.Data())] = ev
	}
	b.sub.AssertEventChanLen(t, 0)

	assertExpiration := func(t *testing.T, ev *subscriber.RouteEvent) {
		t.Helper()
		exp, ok := ev.Extensions()["expiration"].(string)
		require.True(t, ok, "expiration missing from cloud event: %v", ev)
		expiration, err := time.Parse(time.RFC3339, exp)
		require.NoError(t, err)
		assert.WithinRange(t, expiration, before.Add(300*time.Second).Truncate(time.Second), time.Now().UTC().Add(300*time.Second))
	}

	// Entry without its own metadata inherits the request-level metadata.
	require.Contains(t, events, "one")
	assert.Equal(t, "request-type", events["one"].Type())
	assertExpiration(t, events["one"])

	// Entry with its own metadata still inherits request-level keys, but its
	// own keys take precedence.
	require.Contains(t, events, "two")
	assert.Equal(t, "entry-type", events["two"].Type())
	assertExpiration(t, events["two"])
}
