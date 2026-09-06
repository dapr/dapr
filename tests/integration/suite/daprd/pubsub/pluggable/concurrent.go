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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(concurrent))
}

// numInFlight is the number of messages that must be handled at the same time
// for the test to pass. Two is enough to tell concurrent dispatch apart from
// serial dispatch.
const numInFlight = 2

// concurrent ensures that messages received from a pluggable pub/sub component
// are dispatched to the application concurrently, so that more than one message
// can be in flight per subscription. Dispatching them inline caps a
// subscription at the inverse of the per-message latency.
type concurrent struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	inFlight    atomic.Int64
	allInFlight chan struct{}
	closeOnce   sync.Once
}

func (c *concurrent) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.allInFlight = make(chan struct{})

	// Every event handler blocks until all of them have been entered, so the
	// subscription can only make progress if the runtime dispatches messages
	// concurrently.
	app := app.New(t,
		app.WithOnTopicEventFn(func(ctx context.Context, _ *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			if c.inFlight.Add(1) == numInFlight {
				c.closeOnce.Do(func() { close(c.allInFlight) })
			}
			select {
			case <-c.allInFlight:
			case <-ctx.Done():
			}
			return new(rtv1.TopicEventResponse), nil
		}),
	)

	c.broker = broker.New(t)

	c.daprd = daprd.New(t,
		c.broker.DaprdOptions(t, "mypub",
			daprd.WithAppPort(app.Port(t)),
			daprd.WithAppProtocol("grpc"),
			daprd.WithResourceFiles(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: mysub
spec:
  pubsubname: mypub
  topic: a
  routes:
    default: /a
`),
		)...,
	)

	return []framework.Option{
		framework.WithProcesses(app, c.broker, c.daprd),
	}
}

func (c *concurrent) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	require.Len(t, c.daprd.GetMetaSubscriptions(t, ctx), 1)

	for range numInFlight {
		c.broker.PublishHelloWorld("a")
	}

	tctx, cancel := context.WithTimeout(ctx, time.Second*15)
	defer cancel()

	select {
	case <-c.allInFlight:
	case <-tctx.Done():
		assert.Fail(t, "messages were not dispatched concurrently",
			"expected %d messages to be in flight at once, got %d", numInFlight, c.inFlight.Load())
	}
}
