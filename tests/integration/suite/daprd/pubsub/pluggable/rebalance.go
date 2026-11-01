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

package pluggable

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rebalance))
}

type rebalance struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	attempts atomic.Int64
}

func (r *rebalance) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	app := app.New(t,
		app.WithSubscribe(`[{"pubsubname":"mypub","topic":"a","route":"/a"}]`),
		app.WithHandlerFunc("/a", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			r.attempts.Add(1)
			w.WriteHeader(nethttp.StatusInternalServerError)
		}),
	)

	r.broker = broker.New(t)

	r.daprd = daprd.New(t,
		r.broker.DaprdOptions(t, "mypub",
			daprd.WithAppPort(app.Port()),
			daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: pubsub-retry
spec:
  policies:
    retries:
      pubsubRetry:
        duration: 2s
        maxRetries: 20
        policy: constant
  targets:
    components:
      mypub:
        inbound:
          retry: pubsubRetry
`),
		)...,
	)

	return []framework.Option{
		framework.WithProcesses(app, r.broker, r.daprd),
	}
}

func (r *rebalance) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	r.broker.PublishHelloWorld("a")

	// Wait until the retry loop is properly under way.
	require.Eventually(t, func() bool {
		return r.attempts.Load() >= 2
	}, time.Second*30, time.Millisecond*10,
		"the app should be retried on the poison message")

	// The rebalance.
	r.broker.DropStream()

	// The retry loop owns a message the component no longer holds, so it must
	// stop. Look for a window with no new delivery attempts; with the runner
	// rooted at a context the component cannot cancel, attempts keep landing
	// every 2s for the whole 40s retry budget.
	var quiet bool
	deadline := time.Now().Add(time.Second * 30)
	for time.Now().Before(deadline) {
		before := r.attempts.Load()
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second * 6):
		}
		if r.attempts.Load() == before {
			quiet = true
			break
		}
	}

	require.True(t, quiet,
		"app was still being retried after the component cancelled the handler context: the inbound resiliency runner is ignoring it")
}
