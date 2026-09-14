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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	fakepostman "github.com/dapr/dapr/pkg/runtime/subscription/postman/fake"
)

// TestRebalanceCancelsInboundDelivery covers dapr/components-contrib#4580.
//
// The context a component hands the inbound handler is that component's own
// subscription context - for Kafka it is literally sarama's session.Context(),
// which is cancelled the moment the consumer group starts a rebalance. If the
// inbound resiliency runner does not derive from it, neither an in-flight app
// invocation nor a retry backoff can be interrupted, ConsumeClaim cannot return,
// the member is evicted, and a permanently-failing message oscillates between
// consumers forever.
func TestRebalanceCancelsInboundDelivery(t *testing.T) {
	// deliverAndCancel delivers one message, waits until the app has been
	// invoked, then cancels the context the component handed the handler
	// (the rebalance). It returns the handler's error, or fails the test if
	// the handler is still blocked 2s later.
	deliverAndCancel := func(t *testing.T, prov resiliency.Provider, pubsubName string, deliverFn func(context.Context, *runtimePubsub.SubscribedMessage) error, invoked <-chan struct{}) error {
		t.Helper()

		comp := newPausablePubSub()
		require.NoError(t, comp.Init(t.Context(), contribpubsub.Metadata{}))

		_, err := New(Options{
			Resiliency: prov,
			Postman:    fakepostman.New().WithDeliverFn(deliverFn),
			PubSub:     &runtimePubsub.PubsubItem{Component: comp},
			AppID:      TestRuntimeConfigID,
			PubSubName: pubsubName,
			Topic:      "topic0",
			Route: runtimePubsub.Subscription{
				Rules: []*runtimePubsub.Rule{{Path: "orders"}},
			},
		})
		require.NoError(t, err)

		// Stand-in for sarama's session.Context().
		sessionCtx, rebalance := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- comp.deliver(sessionCtx, "topic0", []byte(`{"data":"x"}`))
		}()

		select {
		case <-invoked:
		case <-time.After(time.Second * 5):
			t.Fatal("the app was never invoked")
		}

		rebalance()

		select {
		case err := <-done:
			return err
		case <-time.After(time.Second * 2):
			t.Fatal("handler still running 2s after the component cancelled its context: the inbound resiliency runner is ignoring it")
			return nil
		}
	}

	// A slow app: the in-flight invocation must be cancellable. No resiliency
	// policy is involved, so this isolates the context plumbing itself.
	t.Run("in-flight app invocation", func(t *testing.T) {
		invoked := make(chan struct{})
		var once sync.Once

		err := deliverAndCancel(t, resiliency.New(log), "testpubsub",
			func(ctx context.Context, _ *runtimePubsub.SubscribedMessage) error {
				once.Do(func() { close(invoked) })
				<-ctx.Done()
				return ctx.Err()
			}, invoked)

		require.ErrorIs(t, err, context.Canceled)
	})

	// A poison message under a long retry policy: the backoff sleep between
	// attempts must be cancellable. This is the issue's exact configuration,
	// scaled down.
	t.Run("retry backoff between attempts", func(t *testing.T) {
		prov := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, "5m",
			resiliencyV1alpha.Retry{
				Policy:     "constant",
				Duration:   "40s",
				MaxRetries: new(5),
			})

		invoked := make(chan struct{})
		var once sync.Once

		err := deliverAndCancel(t, prov, pubsubName,
			func(_ context.Context, _ *runtimePubsub.SubscribedMessage) error {
				once.Do(func() { close(invoked) })
				return rterrors.NewRetriable(errors.New("app returned 500"))
			}, invoked)

		require.ErrorIs(t, err, context.Canceled)
	})
}
