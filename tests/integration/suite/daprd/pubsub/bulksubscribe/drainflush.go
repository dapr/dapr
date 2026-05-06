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
	suite.Register(new(drainFlush))
}

// drainFlush verifies that the defaultBulkSubscriber flushes partial batches
// promptly via drain-and-flush rather than waiting for the await-duration
// timer. This is the regression test for dapr/dapr#9727.
//
// Strategy: configure a very long maxAwaitDurationMs (10s) and a large
// maxMessagesCount (100). Publish a small number of messages (5). With
// drain-and-flush, all messages should be delivered well before the timer
// fires. Without it, the messages would sit in the buffer for 10s.
type drainFlush struct {
	daprd  *daprd.Daprd
	app    *app.App
	client *http.Client

	mu        sync.Mutex
	totalMsgs int
}

func (df *drainFlush) Setup(t *testing.T) []framework.Option {
	df.client = client.HTTP(t)

	df.app = app.New(t,
		app.WithHandlerFunc("/drain-bulk", func(w http.ResponseWriter, r *http.Request) {
			var env rtpubsub.BulkSubscribeEnvelope
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&env))

			df.mu.Lock()
			df.totalMsgs += len(env.Entries)
			df.mu.Unlock()

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

	df.daprd = daprd.New(t,
		daprd.WithAppPort(df.app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: drainpub
spec:
 type: pubsub.in-memory
 version: v1
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: bulkdrain
spec:
 pubsubname: drainpub
 topic: drain-test
 routes:
  default: /drain-bulk
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 100
  maxAwaitDurationMs: 10000
`))

	return []framework.Option{
		framework.WithProcesses(df.app, df.daprd),
	}
}

func (df *drainFlush) Run(t *testing.T, ctx context.Context) {
	df.daprd.WaitUntilRunning(t, ctx)

	const numMessages = 5

	start := time.Now()

	// Publish messages one at a time. With the in-memory component
	// (async delivery) and drain-and-flush, each message should be
	// flushed as soon as it is received, without waiting for the 10s timer.
	for i := range numMessages {
		df.publish(t, ctx, fmt.Sprintf(`{"id": %d}`, i))
	}

	// Wait until all messages are delivered.
	require.Eventually(t, func() bool {
		df.mu.Lock()
		defer df.mu.Unlock()
		return df.totalMsgs >= numMessages
	}, 5*time.Second, 50*time.Millisecond,
		"expected %d messages to be delivered", numMessages)

	elapsed := time.Since(start)

	// All messages should be delivered well under the 10s timer.
	// Without drain-and-flush, they'd wait for the full timer.
	// Use 5s (half of maxAwaitDurationMs) to allow for CI overhead.
	assert.Less(t, elapsed, 5*time.Second,
		"messages should be delivered promptly via drain-and-flush, not waiting for the 10s timer")

	df.mu.Lock()
	assert.Equal(t, numMessages, df.totalMsgs, "all messages should be delivered")
	df.mu.Unlock()
}

func (df *drainFlush) publish(t *testing.T, ctx context.Context, data string) {
	t.Helper()

	reqURL := "http://" + df.daprd.HTTPAddress() + "/v1.0/publish/drainpub/drain-test"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL,
		strings.NewReader(data))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := df.client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
