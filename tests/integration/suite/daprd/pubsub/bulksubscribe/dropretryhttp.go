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
	suite.Register(new(dropRetryHTTP))
}

// dropRetryHTTP covers an HTTP app dropping an entry on a retry attempt,
// which only carries the entries that failed before. The dropped entry is the
// last of the bulk message, so looked up by its index in the whole message it
// sits past the end of the retry attempt's entries: the right entry must be
// dead lettered, and daprd must not panic.
type dropRetryHTTP struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
	sub    *subscriber.Subscriber
	calls  atomic.Int32
}

func (d *dropRetryHTTP) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	d.broker = broker.New(t, broker.WithRecordPublishes())
	d.sub = subscriber.New(t,
		subscriber.WithBulkRoutes("/bulk"),
		subscriber.WithBulkStatusFn(func(_ context.Context, ids []string) map[string]contribpubsub.AppResponseStatus {
			if d.calls.Add(1) == 1 {
				return map[string]contribpubsub.AppResponseStatus{
					ids[2]: contribpubsub.Retry,
					ids[3]: contribpubsub.Retry,
				}
			}
			return map[string]contribpubsub.AppResponseStatus{
				ids[len(ids)-1]: contribpubsub.Drop,
			}
		}),
	)

	d.daprd = daprd.New(t, d.broker.DaprdOptions(t, "mypub",
		daprd.WithAppPort(d.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: mysub
spec:
 pubsubname: mypub
 topic: dropretry
 deadLetterTopic: deadletter
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
  retries:
   bulkRetry:
    policy: constant
    duration: 10ms
    maxRetries: 3
 targets:
  components:
   mypub:
    inbound:
     retry: bulkRetry
`),
	)...)

	return []framework.Option{
		framework.WithProcesses(d.sub, d.broker, d.daprd),
	}
}

func (d *dropRetryHTTP) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	ids := []string{"e0", "e1", "e2", "e3"}
	d.broker.PublishCloudEvents(t, ctx, "dropretry", ids...)

	assert.Equal(t, ids, d.sub.ReceiveBulkEventIDs(t, ctx))
	assert.Equal(t, []string{"e2", "e3"}, d.sub.ReceiveBulkEventIDs(t, ctx))

	topic, id := d.broker.NextPublishedCloudEvent(t, ctx)
	assert.Equal(t, "deadletter", topic)
	assert.Equal(t, "e3", id)

	assert.Equal(t, map[string]string{"e0": "", "e1": "", "e2": "", "e3": ""}, d.broker.Acks(t, ctx, len(ids)))
}
