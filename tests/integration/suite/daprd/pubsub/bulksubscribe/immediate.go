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

package bulksubscribe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	compv1 "github.com/dapr/dapr/pkg/proto/components/v1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/tests/integration/framework"
	frameworkos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	pluggable "github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemorypluggable "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(immediate))
}

// immediate verifies that when the upstream pubsub component declares
// pubsub.FeatureBulkSubscribeImmediate (e.g. Pulsar), the default bulk
// subscriber takes the flush-on-arrival path: each delivered message
// reaches the app handler well before the configured
// maxAwaitDurationMs would have elapsed. Regression test for
// dapr/dapr#9727.
//
// We use the pluggable pubsub framework to inject the feature on top
// of an in-memory implementation. The real `pubsub.in-memory`
// component does not declare the feature (and shouldn't, because the
// other in-memory bulk-subscribe tests rely on the batching contract).
// Messages are injected via the PullMessages stream channel, which is
// the delivery path the pluggable component exposes to daprd.
type immediate struct {
	daprd     *daprd.Daprd
	app       *app.App
	plug      *pluggable.PubSub
	socket    *socket.Socket
	pmrReqCh  chan *compv1.PullMessagesRequest
	pmrRespCh chan *compv1.PullMessagesResponse

	mu         sync.Mutex
	deliveries []delivery
}

func (im *immediate) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	im.socket = socket.New(t)
	im.pmrReqCh = make(chan *compv1.PullMessagesRequest, 16)
	im.pmrRespCh = make(chan *compv1.PullMessagesResponse, 16)

	im.plug = pluggable.New(t,
		pluggable.WithSocket(im.socket),
		pluggable.WithPullMessagesChannel(im.pmrReqCh, im.pmrRespCh),
		pluggable.WithPubSub(inmemorypluggable.NewWrappedInMemory(t,
			inmemorypluggable.WithFeatures(
				contribpubsub.FeatureBulkPublish,
				contribpubsub.FeatureMessageTTL,
				contribpubsub.FeatureSubscribeWildcards,
				contribpubsub.FeatureBulkSubscribeImmediate,
			),
		)),
	)

	im.app = app.New(t,
		app.WithHandlerFunc("/immediate-bulk", func(w http.ResponseWriter, r *http.Request) {
			var env rtpubsub.BulkSubscribeEnvelope
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&env))

			im.mu.Lock()
			im.deliveries = append(im.deliveries, delivery{
				time:    time.Now(),
				entries: len(env.Entries),
			})
			im.mu.Unlock()

			type statusT struct {
				EntryID string `json:"entryId"`
				Status  string `json:"status"`
			}
			type respT struct {
				Statuses []statusT `json:"statuses"`
			}
			var resp respT
			for _, entry := range env.Entries {
				resp.Statuses = append(resp.Statuses, statusT{
					EntryID: entry.EntryId,
					Status:  "SUCCESS",
				})
			}
			assert.NoError(t, json.NewEncoder(w).Encode(resp))
		}),
	)

	im.daprd = daprd.New(t,
		daprd.WithAppPort(im.app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithSocket(t, im.socket),
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: immediatepub
spec:
 type: pubsub.%s
 version: v1
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: bulkimmediate
spec:
 pubsubname: immediatepub
 topic: immediate-test
 routes:
  default: /immediate-bulk
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 100
  maxAwaitDurationMs: 5000
`, im.plug.SocketName())))

	return []framework.Option{
		framework.WithProcesses(im.app, im.plug, im.daprd),
	}
}

func (im *immediate) Run(t *testing.T, ctx context.Context) {
	im.daprd.WaitUntilRunning(t, ctx)

	const numMessages = 3

	// CloudEvent envelope shape mirrors the one PublishHelloWorld
	// uses in the broker test fixture — it is what the dapr pubsub
	// pipeline expects to decode for downstream delivery to the app.
	cloudEvent := func(id int) []byte {
		return fmt.Appendf(nil,
			`{"data":{"id":%d},"datacontenttype":"application/json","id":"msg-%d","pubsubname":"immediatepub","source":"test","specversion":"1.0","time":"2026-01-01T00:00:00Z","topic":"immediate-test","traceid":"00-00000000000000000000000000000000-0000000000000000-00","traceparent":"00-00000000000000000000000000000000-0000000000000000-00","tracestate":"","type":"com.dapr.event.sent"}`,
			id, id)
	}

	start := time.Now()
	for i := range numMessages {
		select {
		case im.pmrRespCh <- &compv1.PullMessagesResponse{
			Data:      cloudEvent(i),
			Id:        fmt.Sprintf("msg-%d", i),
			TopicName: "immediate-test",
		}:
		case <-ctx.Done():
			t.Fatal("context cancelled while injecting messages")
		case <-time.After(2 * time.Second):
			t.Fatal("timed out injecting message into pluggable pubsub")
		}
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		im.mu.Lock()
		defer im.mu.Unlock()
		require.Len(c, im.deliveries, numMessages, "expected one delivery per injected message")
	}, 10*time.Second, 50*time.Millisecond)

	elapsed := time.Since(start)

	// All three deliveries must arrive far below the 5 s batching
	// timer. 3 s leaves comfortable margin for daprd's own
	// scheduling overhead while still failing if the immediate path
	// were not selected.
	require.Less(t, elapsed, 3*time.Second,
		"deliveries under FeatureBulkSubscribeImmediate should not wait for the 5s timer; got %v", elapsed)

	im.mu.Lock()
	defer im.mu.Unlock()
	for i, d := range im.deliveries {
		require.Equal(t, 1, d.entries, "delivery %d should carry exactly one entry", i)
	}
}
