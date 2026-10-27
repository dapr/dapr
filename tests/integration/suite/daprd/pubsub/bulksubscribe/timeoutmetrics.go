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
	suite.Register(new(timeoutMetrics))
}

// timeoutMetrics covers the bulk ingress metrics of a delivery whose first
// attempt times out. The timed out attempt and the one that replaces it share
// the delivery's diagnostics: the abandoned attempt counts its entries as
// retries, the replacement counts them as successes, and the delivery is
// reported once.
type timeoutMetrics struct {
	daprd  *daprd.Daprd
	broker *broker.Broker
	sub    *subscriber.Subscriber
	calls  atomic.Int32
}

func (m *timeoutMetrics) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	m.broker = broker.New(t)
	m.sub = subscriber.New(t,
		subscriber.WithBulkRoutes("/bulk"),
		subscriber.WithBulkStatusFn(func(ctx context.Context, ids []string) map[string]contribpubsub.AppResponseStatus {
			if m.calls.Add(1) == 1 {
				// Hold the first attempt until daprd gives up on it.
				select {
				case <-ctx.Done():
				case <-time.After(10 * time.Second):
				}
			}
			return nil
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
 topic: timeoutmetrics
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
    duration: 100ms
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
		framework.WithProcesses(m.sub, m.broker, m.daprd),
	}
}

func (m *timeoutMetrics) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	ids := []string{"e0", "e1", "e2", "e3"}
	m.broker.PublishCloudEvents(t, ctx, "timeoutmetrics", ids...)
	assert.Equal(t, map[string]string{"e0": "", "e1": "", "e2": "", "e3": ""}, m.broker.Acks(t, ctx, len(ids)))
	assert.Equal(t, int32(2), m.calls.Load())

	// The event_count metric counts the reports a status occurred in, not the
	// entries with that status.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := m.daprd.Metrics(c, ctx)
		assert.InDelta(c, 1.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_count", "topic:timeoutmetrics"), 0)
		assert.InDelta(c, 1.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:timeoutmetrics", "process_status:SUCCESS"), 0)
		assert.InDelta(c, 1.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:timeoutmetrics", "process_status:RETRY"), 0)
		assert.InDelta(c, 0.0, metrics.SumWithLabels("dapr_component_pubsub_ingress_bulk_event_count", "topic:timeoutmetrics", "process_status:DROP"), 0)
	}, 10*time.Second, 50*time.Millisecond)
}
