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
	suite.Register(new(retriesexhausted))
}

type retriesexhausted struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	deliveries atomic.Int64
}

func (r *retriesexhausted) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	app := app.New(t,
		app.WithSubscribe(`[{"pubsubname":"mypub","topic":"a","route":"/a"}]`),
		app.WithHandlerFunc("/a", func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			r.deliveries.Add(1)
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
  name: pubsub-bounded-retry
spec:
  policies:
    retries:
      pubsubRetry:
        duration: 10ms
        maxRetries: 2
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

func (r *retriesexhausted) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.daprd.GetMetaSubscriptions(c, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	ackCh := r.broker.PublishHelloWorld("a")

	select {
	case req := <-ackCh:
		require.NotNil(t, req.GetAckError(),
			"a message the app always rejects must be reported as failed")
		assert.Contains(t, req.GetAckError().GetMessage(), "pubsub retries exhausted",
			"an exhausted retry policy with no dead letter topic must tell the component it can stop redelivering")
		assert.Contains(t, req.GetAckError().GetMessage(), "500",
			"the failure that exhausted the policy must survive alongside the sentinel")
	case <-time.After(time.Second * 20):
		assert.Fail(t, "timed out waiting for the message to be acked")
	}

	assert.Equal(t, int64(3), r.deliveries.Load())
}
