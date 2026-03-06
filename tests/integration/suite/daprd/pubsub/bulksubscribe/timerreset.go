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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(timerReset))
}

// timerReset verifies that after a count-based batch flush, the bulk
// subscriber resets its await-duration timer so the next batch gets the full
// window. This is the regression test for dapr/dapr#9211.
type timerReset struct {
	daprd  *daprd.Daprd
	app    *app.App
	client *http.Client

	mu         sync.Mutex
	deliveries []delivery
	deliveryCh chan struct{}
}

type delivery struct {
	time    time.Time
	entries int
}

func (tr *timerReset) Setup(t *testing.T) []framework.Option {
	tr.deliveryCh = make(chan struct{}, 10)
	tr.client = client.HTTP(t)

	tr.app = app.New(t,
		app.WithHandlerFunc("/bulk", func(w http.ResponseWriter, r *http.Request) {
			var env rtpubsub.BulkSubscribeEnvelope
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&env))

			tr.mu.Lock()
			tr.deliveries = append(tr.deliveries, delivery{
				time:    time.Now(),
				entries: len(env.Entries),
			})
			tr.mu.Unlock()

			select {
			case tr.deliveryCh <- struct{}{}:
			default:
			}

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

	tr.daprd = daprd.New(t,
		daprd.WithAppPort(tr.app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mypub
spec:
 type: pubsub.in-memory
 version: v1
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: bulktimer
spec:
 pubsubname: mypub
 topic: timer-test
 routes:
  default: /bulk
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 3
  maxAwaitDurationMs: 1500
`))

	return []framework.Option{
		framework.WithProcesses(tr.app, tr.daprd),
	}
}

func (tr *timerReset) Run(t *testing.T, ctx context.Context) {
	tr.daprd.WaitUntilRunning(t, ctx)

	// Publish maxMessagesCount (3) individual messages to trigger a
	// count-based flush.
	for i := range 3 {
		tr.publish(t, ctx, fmt.Sprintf(`{"id": %d}`, i))
	}

	// Wait for the count-based flush.
	tr.waitForDelivery(t, ctx, "first batch (count-based flush)")

	tr.mu.Lock()
	require.Len(t, tr.deliveries, 1, "expected exactly 1 delivery after 3 messages")
	assert.Equal(t, 3, tr.deliveries[0].entries, "first batch should contain 3 entries")
	firstFlushTime := tr.deliveries[0].time
	tr.mu.Unlock()

	// Immediately publish 1 more message. If the timer was properly reset,
	// this message should NOT be delivered for ~1500ms.
	tr.publish(t, ctx, `{"id": 99}`)

	// Verify the message is NOT delivered prematurely (within 500ms).
	// Without the fix, the old timer would still be running and could fire
	// much sooner than 1500ms.
	select {
	case <-tr.deliveryCh:
		tr.mu.Lock()

		elapsed := tr.deliveries[len(tr.deliveries)-1].time.Sub(firstFlushTime)
		tr.mu.Unlock()

		if elapsed < 800*time.Millisecond {
			t.Fatalf("message was flushed prematurely at %v after count-based flush; "+
				"timer should have been reset to 1500ms", elapsed)
		}
		// If we got here with elapsed >= 800ms, the timer was reset (OK).
	case <-time.After(500 * time.Millisecond):
		// Good — no premature delivery within 500ms.
	}

	// Now wait for the timer-based flush to actually deliver the message.
	tr.waitForDelivery(t, ctx, "second batch (timer-based flush)")

	tr.mu.Lock()
	require.Len(t, tr.deliveries, 2, "expected exactly 2 deliveries total")
	assert.Equal(t, 1, tr.deliveries[1].entries, "second batch should contain 1 entry")

	// The second delivery should happen roughly 1500ms after the first,
	// proving the timer was reset.
	elapsed := tr.deliveries[1].time.Sub(firstFlushTime)
	assert.Greater(t, elapsed, 1000*time.Millisecond,
		"second flush should happen at least 1000ms after count-based flush (timer reset to 1500ms)")
	assert.Less(t, elapsed, 4000*time.Millisecond,
		"second flush should not take excessively long")
	tr.mu.Unlock()
}

func (tr *timerReset) publish(t *testing.T, ctx context.Context, data string) {
	t.Helper()

	reqURL := "http://" + tr.daprd.HTTPAddress() + "/v1.0/publish/mypub/timer-test"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL,
		strings.NewReader(data))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := tr.client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func (tr *timerReset) waitForDelivery(t *testing.T, ctx context.Context, desc string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("timed out waiting for bulk delivery: %s", desc)
	case <-tr.deliveryCh:
	}
}
