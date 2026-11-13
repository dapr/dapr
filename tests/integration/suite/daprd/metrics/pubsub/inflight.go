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

package pubsub

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(inflight))
}

// inflight asserts that the in-flight gauge counts messages the app is still
// processing, is reported per topic rather than per component, and returns to
// zero once the handlers complete.
type inflight struct {
	daprd *daprd.Daprd

	arrived chan string
	release chan struct{}
}

func (i *inflight) Setup(t *testing.T) []framework.Option {
	i.arrived = make(chan string, 2)
	i.release = make(chan struct{})

	// Both handlers park until release is closed, holding their message in
	// flight for as long as the assertions need.
	hold := func(topic string) func(http.ResponseWriter, *http.Request) {
		return func(_ http.ResponseWriter, r *http.Request) {
			select {
			case i.arrived <- topic:
			case <-r.Context().Done():
				return
			}
			select {
			case <-i.release:
			case <-r.Context().Done():
			}
		}
	}

	app := app.New(t,
		app.WithHandlerFunc("/dapr/subscribe", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("content-type", "application/json")
			io.WriteString(w, `[
				{"pubsubname":"foo","topic":"abc","route":"/abc"},
				{"pubsubname":"foo","topic":"def","route":"/def"}
			]`)
		}),
		app.WithHandlerFunc("/abc", hold("abc")),
		app.WithHandlerFunc("/def", hold("def")),
	)

	i.daprd = daprd.New(t,
		daprd.WithAppID("myapp"),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, i.daprd),
	}
}

func (i *inflight) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)

	// Closed on every exit path so a failed assertion cannot wedge the
	// handlers, and so the subscriptions can drain during cleanup.
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(i.release)
		}
	}
	t.Cleanup(releaseOnce)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, i.daprd.GetMetaSubscriptions(c, ctx), 2)
	}, time.Second*10, time.Millisecond*10)

	metricFor := func(topic string) string {
		return "dapr_component_pubsub_ingress_in_flight|app_id:myapp|component:foo|namespace:|topic:" + topic
	}

	client := i.daprd.GRPCClient(t, ctx)
	for _, topic := range []string{"abc", "def"} {
		_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
			PubsubName: "foo",
			Topic:      topic,
			Data:       []byte(`{"status":"processing"}`),
		})
		require.NoError(t, err)
	}

	for range 2 {
		select {
		case <-i.arrived:
		case <-time.After(time.Second * 10):
			require.Fail(t, "app did not receive both messages")
		}
	}

	// Both handlers are parked, so each topic should report one in flight.
	// The gauge is per topic even though the two share one component.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := i.daprd.Metrics(c, ctx).All()
		assert.Equal(c, 1, int(metrics[metricFor("abc")]))
		assert.Equal(c, 1, int(metrics[metricFor("def")]))
	}, time.Second*10, time.Millisecond*10)

	releaseOnce()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := i.daprd.Metrics(c, ctx).All()
		assert.Equal(c, 0, int(metrics[metricFor("abc")]))
		assert.Equal(c, 0, int(metrics[metricFor("def")]))
	}, time.Second*10, time.Millisecond*10)
}
