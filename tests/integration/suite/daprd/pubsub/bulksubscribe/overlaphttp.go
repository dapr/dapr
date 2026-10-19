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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/tests/integration/framework"
	frameworkos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(overlapHTTP))
}

// overlapHTTP covers a bulk delivery to an HTTP app whose first attempt
// outlives the resiliency timeout. The runner abandons that attempt and
// retries straight away, so the abandoned attempt is still winding down while
// its replacement is delivered and accumulated. The replacement must carry
// every entry again, the attempt after it only the entries asked to retry,
// and every message is acked once they succeed.
type overlapHTTP struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
	sub    *subscriber.Subscriber
	calls  atomic.Int32
}

func (o *overlapHTTP) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	o.broker = broker.New(t)
	o.sub = subscriber.New(t,
		subscriber.WithBulkRoutes("/bulk"),
		subscriber.WithBulkStatusFn(func(ctx context.Context, ids []string) map[string]contribpubsub.AppResponseStatus {
			switch o.calls.Add(1) {
			case 1:
				// Hold the first attempt until daprd gives up on it.
				select {
				case <-ctx.Done():
				case <-time.After(10 * time.Second):
				}
			case 2:
				return map[string]contribpubsub.AppResponseStatus{
					ids[1]: contribpubsub.Retry,
					ids[3]: contribpubsub.Retry,
				}
			}
			return nil
		}),
	)

	o.daprd = daprd.New(t, o.broker.DaprdOptions(t, "mypub",
		daprd.WithAppPort(o.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: mysub
spec:
 pubsubname: mypub
 topic: overlap
 routes:
  default: /bulk
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 4
  maxAwaitDurationMs: 30000
`, `apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
 name: myresiliency
spec:
 policies:
  timeouts:
   bulkTimeout: 500ms
  retries:
   bulkRetry:
    policy: constant
    duration: 10ms
    maxRetries: 3
 targets:
  components:
   mypub:
    inbound:
     timeout: bulkTimeout
     retry: bulkRetry
`),
	)...)

	return []framework.Option{
		framework.WithProcesses(o.sub, o.broker, o.daprd),
	}
}

func (o *overlapHTTP) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	ids := []string{"e0", "e1", "e2", "e3"}
	o.broker.PublishCloudEvents(t, ctx, "overlap", ids...)

	assert.Equal(t, ids, o.sub.ReceiveBulkEventIDs(t, ctx), "first attempt")
	assert.Equal(t, ids, o.sub.ReceiveBulkEventIDs(t, ctx), "the timed out attempt is not accumulated")
	assert.Equal(t, []string{"e1", "e3"}, o.sub.ReceiveBulkEventIDs(t, ctx), "only the entries asked to retry")
	assert.Equal(t, map[string]string{"e0": "", "e1": "", "e2": "", "e3": ""}, o.broker.Acks(t, ctx, len(ids)))
	o.sub.AssertBulkEventChanLen(t, 0)
}
