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

package subscription

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman/http"
)

const inFlightMetric = "component/pubsub_ingress/in_flight"

// readInFlight returns the last recorded value of the in-flight gauge, or -1
// if nothing has been recorded yet. It is called from require.Eventually's
// goroutine, so it reports failure by value rather than failing the test.
func readInFlight(meter view.Meter) int64 {
	rows, err := meter.RetrieveData(inFlightMetric)
	if err != nil || len(rows) == 0 {
		return -1
	}
	return int64(rows[0].Data.(*view.LastValueData).Value)
}

// newGatedSubscription builds a subscription whose app channel blocks until
// gate is closed, so deliveries can be held in flight.
func newGatedSubscription(t *testing.T, comp contribpubsub.PubSub, gate <-chan struct{}) *Subscription {
	t.Helper()

	resp := contribpubsub.AppResponse{Status: contribpubsub.Success}
	respB, err := json.Marshal(resp)
	require.NoError(t, err)

	mockAppChannel := new(channelt.MockAppChannel)
	mockAppChannel.Init()
	// Each call gets its own response: the body is a single-use reader, so a
	// shared one races across concurrent deliveries.
	mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).
		Run(func(mock.Arguments) {
			<-gate
		}).
		Return(func(context.Context, *invokev1.InvokeMethodRequest, string) *invokev1.InvokeMethodResponse {
			return invokev1.NewInvokeMethodResponse(200, "OK", nil).
				WithRawDataBytes(respB).
				WithContentType("application/json")
		}, nil)

	require.NoError(t, comp.Init(t.Context(), contribpubsub.Metadata{}))

	sub, err := New(Options{
		Resiliency: resiliency.New(log),
		Postman: http.New(http.Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		}),
		PubSub:     &runtimePubsub.PubsubItem{Component: comp},
		AppID:      TestRuntimeConfigID,
		PubSubName: "testpubsub",
		Topic:      "topic0",
		Route: runtimePubsub.Subscription{
			Rules: []*runtimePubsub.Rule{{Path: "orders"}},
		},
	})
	require.NoError(t, err)
	return sub
}

// TestInFlightGaugeTracksConcurrentDeliveries is the whole point of the
// metric: while N handlers are blocked the gauge reads N, which is the number
// an operator needs to size the component's concurrency setting against.
func TestInFlightGaugeTracksConcurrentDeliveries(t *testing.T) {
	meter := view.NewMeter()
	meter.Start()
	// Deliberately not stopped: DefaultComponentMonitoring is a package-level
	// global, so a stopped meter would break recording for every later test in
	// this package.
	require.NoError(t, diag.DefaultComponentMonitoring.Init(
		meter, TestRuntimeConfigID, "default",
		config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log),
	))

	gate := make(chan struct{})
	comp := newPausablePubSub()
	sub := newGatedSubscription(t, comp, gate)

	const concurrent = 3
	var wg sync.WaitGroup
	for range concurrent {
		wg.Go(func() {
			_ = comp.deliver(t.Context(), "topic0", []byte(`{"id":"1"}`))
		})
	}

	require.Eventually(t, func() bool {
		return readInFlight(meter) == concurrent
	}, 5*time.Second, 10*time.Millisecond, "gauge should report every blocked handler")

	close(gate)
	wg.Wait()

	require.Eventually(t, func() bool {
		return readInFlight(meter) == 0
	}, 5*time.Second, 10*time.Millisecond, "gauge should return to zero once handlers resolve")

	sub.Stop()
	assert.Equal(t, int64(0), readInFlight(meter))
}
