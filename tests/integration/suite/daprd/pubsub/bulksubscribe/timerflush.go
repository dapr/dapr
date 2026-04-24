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
	suite.Register(new(timerFlush))
}

// timerFlush verifies that messages below the count threshold are NOT
// flushed on arrival (no drain-and-flush regression). In the in-memory
// pubsub (transactional), deliveries are serialised, so n published
// messages produce n batches of 1, each held until the per-message
// awaitDuration timer fires. The check here is that the first delivery
// does not happen instantaneously — i.e. the subscriber is not receiving
// messages one-by-one as they arrive without regard for the timer.
type timerFlush struct {
	daprd  *daprd.Daprd
	app    *app.App
	client *http.Client

	mu         sync.Mutex
	deliveries []delivery
}

func (tf *timerFlush) Setup(t *testing.T) []framework.Option {
	tf.client = client.HTTP(t)

	tf.app = app.New(t,
		app.WithHandlerFunc("/timer-bulk", func(w http.ResponseWriter, r *http.Request) {
			var env rtpubsub.BulkSubscribeEnvelope
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&env))

			tf.mu.Lock()
			tf.deliveries = append(tf.deliveries, delivery{
				time:    time.Now(),
				entries: len(env.Entries),
			})
			tf.mu.Unlock()

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

	tf.daprd = daprd.New(t,
		daprd.WithAppPort(tf.app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: timerpub
spec:
 type: pubsub.in-memory
 version: v1
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: bulktimerflush
spec:
 pubsubname: timerpub
 topic: timer-flush-test
 routes:
  default: /timer-bulk
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 100
  maxAwaitDurationMs: 500
`))

	return []framework.Option{
		framework.WithProcesses(tf.app, tf.daprd),
	}
}

func (tf *timerFlush) Run(t *testing.T, ctx context.Context) {
	tf.daprd.WaitUntilRunning(t, ctx)

	const (
		awaitDurationMs = 500
		numMessages     = 2
	)

	// Fire publishes concurrently so they land close together; with
	// in-memory (transactional) the second one blocks waiting for the
	// first's handler to complete, so effectively the pubsub delivers
	// them serially — but the kickoff happens at the same instant.
	start := time.Now()
	var wg sync.WaitGroup
	for i := range numMessages {
		wg.Go(func() {
			tf.publish(t, ctx, fmt.Sprintf(`{"id": %d}`, i))
		})
	}
	wg.Wait()

	// Wait for both deliveries; they must land at roughly awaitDuration
	// intervals.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		tf.mu.Lock()
		defer tf.mu.Unlock()
		require.Len(c, tf.deliveries, numMessages, "expected exactly %d deliveries", numMessages)
	}, time.Duration(awaitDurationMs*numMessages*3)*time.Millisecond, 20*time.Millisecond)

	tf.mu.Lock()
	defer tf.mu.Unlock()

	// Guard against drain-and-flush regression: the FIRST delivery must
	// not happen instantaneously — a proper timer-based flush waits at
	// least close to one awaitDuration window.
	firstDeliveryElapsed := tf.deliveries[0].time.Sub(start)
	lowerBound := time.Duration(awaitDurationMs) * time.Millisecond * 7 / 10
	require.GreaterOrEqual(t, firstDeliveryElapsed, lowerBound,
		"first delivery must be ~awaitDuration after publish (no drain-and-flush); got %v", firstDeliveryElapsed)

	// Each delivery should contain exactly 1 entry (serial delivery via
	// in-memory pubsub).
	for i, d := range tf.deliveries {
		require.Equal(t, 1, d.entries, "delivery %d should contain 1 entry", i)
	}

	// The SECOND delivery must be roughly awaitDuration after the first
	// — not clustered (which would hint at a stale timer firing for
	// multiple messages) and not much later (would hint at a loop stall).
	gap := tf.deliveries[1].time.Sub(tf.deliveries[0].time)
	gapLower := time.Duration(awaitDurationMs) * time.Millisecond * 5 / 10
	gapUpper := time.Duration(awaitDurationMs*3) * time.Millisecond
	require.GreaterOrEqual(t, gap, gapLower,
		"second delivery should be ~awaitDuration after first; got %v", gap)
	require.Less(t, gap, gapUpper,
		"second delivery should not be much later than awaitDuration after first; got %v", gap)
}

func (tf *timerFlush) publish(t *testing.T, ctx context.Context, data string) {
	t.Helper()

	reqURL := "http://" + tf.daprd.HTTPAddress() + "/v1.0/publish/timerpub/timer-flush-test"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL,
		strings.NewReader(data))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := tf.client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
