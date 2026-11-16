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

	"github.com/dapr/dapr/tests/integration/framework"
	frameworkos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rawPayloadMetrics))
}

// rawPayloadMetrics covers the bulk ingress metrics of a bulk message that
// fails before any entry is delivered, because the subscription's rawPayload
// metadata is not a boolean, with no dead letter topic to fall back on. None
// of the entries were counted by a delivery, so they are all counted as
// retries here, and the component is told every entry failed.
type rawPayloadMetrics struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
	sub    *subscriber.Subscriber
}

func (m *rawPayloadMetrics) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	m.broker = broker.New(t)
	m.sub = subscriber.New(t, subscriber.WithBulkRoutes("/bulk"))

	m.daprd = daprd.New(t, m.broker.DaprdOptions(t, "mypub",
		daprd.WithAppPort(m.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: mysub
spec:
 pubsubname: mypub
 topic: rawpayloadmetrics
 metadata:
  rawPayload: notabool
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

func (m *rawPayloadMetrics) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	ids := []string{"e0", "e1", "e2", "e3"}
	m.broker.PublishCloudEvents(t, ctx, "rawpayloadmetrics", ids...)

	acks := m.broker.Acks(t, ctx, len(ids))
	for _, id := range ids {
		assert.NotEmpty(t, acks[id], "entry %s must be acked with an error", id)
	}
	m.sub.AssertBulkEventChanLen(t, 0)

	// The event_count metric counts the reports a status occurred in, not the
	// entries with that status.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.daprd.Metrics(c, ctx)
		assert.InDelta(c, 1.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_count", "topic:rawpayloadmetrics"), 0)
		assert.InDelta(c, 1.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:rawpayloadmetrics", "process_status:RETRY"), 0)
		assert.InDelta(c, 0.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:rawpayloadmetrics", "process_status:DROP"), 0)
		assert.InDelta(c, 0.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:rawpayloadmetrics", "process_status:SUCCESS"), 0)
	}, 10*time.Second, 50*time.Millisecond)
}
