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
	suite.Register(new(retriesexcluded))
}

type retriesexcluded struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	deliveries atomic.Int64
}

func (r *retriesexcluded) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	app := app.New(t,
		app.WithSubscribe(`[{"pubsubname":"mypub","topic":"a","route":"/a"}]`),
		app.WithHandlerFunc("/a", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			r.deliveries.Add(1)
			// 503 is outside the matching rules below, so the policy will not retry
			// it.
			w.WriteHeader(nethttp.StatusServiceUnavailable)
		}),
	)

	r.broker = broker.New(t)

	r.daprd = daprd.New(t,
		r.broker.DaprdOptions(t, "mypub",
			daprd.WithAppPort(app.Port()),
			daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: pubsub-matching-retry
spec:
  policies:
    retries:
      pubsubRetry:
        policy: constant
        duration: 10ms
        maxRetries: 3
        matching:
          httpStatusCodes: "500"
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

func (r *retriesexcluded) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	ackCh := r.broker.PublishHelloWorld("a")

	select {
	case req := <-ackCh:
		require.NotNil(t, req.GetAckError(), "the delivery failed, so it must be reported as failed")
		assert.NotContains(t, req.GetAckError().GetMessage(), "pubsub retries exhausted",
			"a status excluded by the retry condition is not an exhausted budget, and the message must stay redeliverable")
	case <-time.After(time.Second * 20):
		assert.Fail(t, "timed out waiting for the message to be acked")
	}

	assert.Equal(t, int64(1), r.deliveries.Load(),
		"an excluded status must stop the retry loop after the first attempt")
}
