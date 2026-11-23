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
	suite.Register(new(dropRoutes))
}

// dropRoutes covers an app dropping an entry of a bulk message that routing
// rules split across two paths, so each delivery carries only some of the
// message's entries. The app drops the first entry of each delivery: e1 is
// the first entry delivered to /odd but the second of the bulk message, so
// looked up by its index in the whole message it would dead letter e3.
type dropRoutes struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
	sub    *subscriber.Subscriber
}

func (d *dropRoutes) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	d.broker = broker.New(t, broker.WithRecordPublishes())
	d.sub = subscriber.New(t,
		subscriber.WithBulkRoutes("/odd", "/even"),
		subscriber.WithBulkStatusFn(func(_ context.Context, ids []string) map[string]contribpubsub.AppResponseStatus {
			return map[string]contribpubsub.AppResponseStatus{ids[0]: contribpubsub.Drop}
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
 topic: droproutes
 deadLetterTopic: deadletter
 routes:
  rules:
  - match: 'event.id == "e1" || event.id == "e3"'
    path: /odd
  default: /even
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 4
  maxAwaitDurationMs: 30000
`),
	)...)

	return []framework.Option{
		framework.WithProcesses(d.sub, d.broker, d.daprd),
	}
}

func (d *dropRoutes) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	ids := []string{"e0", "e1", "e2", "e3"}
	d.broker.PublishCloudEvents(t, ctx, "droproutes", ids...)

	// Each route is delivered on its own, in no particular order.
	assert.ElementsMatch(t, [][]string{{"e0", "e2"}, {"e1", "e3"}}, [][]string{
		d.sub.ReceiveBulkEventIDs(t, ctx),
		d.sub.ReceiveBulkEventIDs(t, ctx),
	})

	deadLettered := make([]string, 0, 2)
	for range 2 {
		topic, id := d.broker.NextPublishedCloudEvent(t, ctx)
		assert.Equal(t, "deadletter", topic)
		deadLettered = append(deadLettered, id)
	}
	assert.ElementsMatch(t, []string{"e0", "e1"}, deadLettered)

	assert.Equal(t, map[string]string{"e0": "", "e1": "", "e2": "", "e3": ""}, d.broker.Acks(t, ctx, len(ids)))
}
