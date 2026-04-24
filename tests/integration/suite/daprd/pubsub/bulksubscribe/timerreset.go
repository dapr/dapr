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

// timerReset is an end-to-end smoke test for dapr/dapr#9211: it verifies
// that a single published message reaches the subscriber app via the bulk
// subscribe pipeline and is delivered within a predictable window driven
// by the awaitDuration timer. The real regression cover for the count-
// based flush + ticker reset semantics lives in the unit tests under
// pkg/runtime/pubsub/default_bulksub_test.go; the in-memory pubsub
// component serialises delivery through a transactional event bus, so
// the bursty scenarios that would trigger a count-based flush cannot be
// reproduced through a daprd integration test without a custom broker.
type timerReset struct {
	daprd  *daprd.Daprd
	app    *app.App
	client *http.Client

	mu         sync.Mutex
	deliveries []delivery
}

type delivery struct {
	time    time.Time
	entries int
}

func (tr *timerReset) Setup(t *testing.T) []framework.Option {
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
  maxMessagesCount: 100
  maxAwaitDurationMs: 500
`))

	return []framework.Option{
		framework.WithProcesses(tr.app, tr.daprd),
	}
}

func (tr *timerReset) Run(t *testing.T, ctx context.Context) {
	tr.daprd.WaitUntilRunning(t, ctx)

	const awaitDurationMs = 500

	start := time.Now()
	tr.publish(t, ctx, `{"id": 1}`)

	// With in-memory (transactional) pubsub + defaultBulkSubscriber, a
	// single message is held until the awaitDuration timer fires. It
	// must be delivered in its own batch of 1, at roughly
	// awaitDurationMs after publish.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		require.Len(c, tr.deliveries, 1, "expected exactly one timer-based delivery")
	}, time.Duration(awaitDurationMs*4)*time.Millisecond, 20*time.Millisecond)

	tr.mu.Lock()
	defer tr.mu.Unlock()

	require.Equal(t, 1, tr.deliveries[0].entries, "delivery should contain the single message")

	elapsed := tr.deliveries[0].time.Sub(start)
	lowerBound := time.Duration(awaitDurationMs) * time.Millisecond * 7 / 10
	upperBound := time.Duration(awaitDurationMs*4) * time.Millisecond
	require.GreaterOrEqual(t, elapsed, lowerBound,
		"message should be held until ~awaitDuration (not flushed immediately as with drain-and-flush); got %v", elapsed)
	require.Less(t, elapsed, upperBound,
		"message should be delivered within 4x awaitDuration; got %v", elapsed)
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
