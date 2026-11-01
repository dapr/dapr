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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	frameworkos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(dropRetryGRPC))
}

// dropRetryGRPC is dropRetryHTTP for a gRPC app: dropping the last entry of
// the bulk message on a retry attempt dead letters that entry, without
// indexing past the end of the retry attempt's entries.
type dropRetryGRPC struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
	sub    *subscriber.Subscriber
	calls  atomic.Int32
}

func (d *dropRetryGRPC) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	d.broker = broker.New(t, broker.WithRecordPublishes())
	d.sub = subscriber.New(t,
		subscriber.WithBulkStatusFn(func(_ context.Context, ids []string) map[string]rtv1.TopicEventResponse_TopicEventResponseStatus {
			if d.calls.Add(1) == 1 {
				return map[string]rtv1.TopicEventResponse_TopicEventResponseStatus{
					ids[2]: rtv1.TopicEventResponse_RETRY,
					ids[3]: rtv1.TopicEventResponse_RETRY,
				}
			}
			return map[string]rtv1.TopicEventResponse_TopicEventResponseStatus{
				ids[len(ids)-1]: rtv1.TopicEventResponse_DROP,
			}
		}),
	)

	d.daprd = daprd.New(t, d.broker.DaprdOptions(t, "mypub",
		daprd.WithAppPort(d.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
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

func (d *dropRetryGRPC) Run(t *testing.T, ctx context.Context) {
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
