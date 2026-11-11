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
	suite.Register(new(dlqMetrics))
}

// dlqMetrics covers entries that failed delivery and were then dead lettered
// together. Every entry reaches the dead letter topic, and in the bulk ingress
// metrics dead lettering moves them from the retry count, where delivery put
// them, to the drop count, so the delivery reports drops and no retries.
type dlqMetrics struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
	sub    *subscriber.Subscriber
}

func (m *dlqMetrics) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	m.broker = broker.New(t, broker.WithRecordPublishes())
	m.sub = subscriber.New(t,
		subscriber.WithBulkRoutes("/bulk"),
		subscriber.WithBulkStatusFn(func(_ context.Context, ids []string) map[string]contribpubsub.AppResponseStatus {
			statuses := make(map[string]contribpubsub.AppResponseStatus, len(ids))
			for _, id := range ids {
				statuses[id] = contribpubsub.Retry
			}
			return statuses
		}),
	)

	m.daprd = daprd.New(t, m.broker.DaprdOptions(t, "mypub",
		daprd.WithAppPort(m.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: mysub
spec:
 pubsubname: mypub
 topic: dlqmetrics
 deadLetterTopic: deadletter
 routes:
  default: /bulk
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 4
  maxAwaitDurationMs: 30000
`),
	)...)

	return []framework.Option{
		framework.WithProcesses(m.sub, m.broker, m.daprd),
	}
}

func (m *dlqMetrics) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	ids := []string{"e0", "e1", "e2", "e3"}
	m.broker.PublishCloudEvents(t, ctx, "dlqmetrics", ids...)
	assert.Equal(t, ids, m.sub.ReceiveBulkEventIDs(t, ctx))

	deadLettered := make([]string, 0, len(ids))
	for range ids {
		topic, id := m.broker.NextPublishedCloudEvent(t, ctx)
		assert.Equal(t, "deadletter", topic)
		deadLettered = append(deadLettered, id)
	}
	assert.ElementsMatch(t, ids, deadLettered)

	assert.Equal(t, map[string]string{"e0": "", "e1": "", "e2": "", "e3": ""}, m.broker.Acks(t, ctx, len(ids)))

	// The event_count metric counts the reports a status occurred in, not the
	// entries with that status.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.daprd.Metrics(c, ctx)
		assert.InDelta(c, 1.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_count", "topic:dlqmetrics"), 0)
		assert.InDelta(c, 1.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:dlqmetrics", "process_status:DROP"), 0)
		assert.InDelta(c, 0.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:dlqmetrics", "process_status:RETRY"), 0)
		assert.InDelta(c, 0.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:dlqmetrics", "process_status:SUCCESS"), 0)
	}, 10*time.Second, 50*time.Millisecond)
}
