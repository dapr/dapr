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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	fakepostman "github.com/dapr/dapr/pkg/runtime/subscription/postman/fake"
)

// The resiliency policy provider in this package binds its policies to a
// component of this name.
const resiliencyPubsubName = "pubsubName"

type handlerCtxKey struct{}

// newSubscriptionWithPostman builds a subscription that delivers through the
// given postman, so a test can inspect the context the delivery is handed.
func newSubscriptionWithPostman(t *testing.T, comp contribpubsub.PubSub, pm postman.Interface, res *resiliency.Resiliency) *Subscription {
	t.Helper()

	require.NoError(t, comp.Init(t.Context(), contribpubsub.Metadata{}))

	if res == nil {
		res = resiliency.New(log)
	}

	sub, err := New(Options{
		Resiliency: res,
		Postman:    pm,
		PubSub:     &runtimePubsub.PubsubItem{Component: comp},
		AppID:      TestRuntimeConfigID,
		PubSubName: resiliencyPubsubName,
		Topic:      "topic0",
		Route: runtimePubsub.Subscription{
			Rules: []*runtimePubsub.Rule{{Path: "orders"}},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { sub.Stop() })

	return sub
}

// TestDeliveryInheritsHandlerContext covers seeding the resiliency runner with
// the inbound handler context rather than context.Background(). Seeded with
// context.Background() the delivery is severed from the message it belongs to:
// it carries none of the handler context's values, none of its deadline, and
// keeps retrying after the handler context is cancelled.
func TestDeliveryInheritsHandlerContext(t *testing.T) {
	t.Run("the delivery context carries the handler context's values", func(t *testing.T) {
		comp := newPausablePubSub()

		var delivered atomic.Value

		pm := fakepostman.New().WithDeliverFn(func(ctx context.Context, _ *runtimePubsub.SubscribedMessage) error {
			value, _ := ctx.Value(handlerCtxKey{}).(string)
			delivered.Store(value)

			return nil
		})

		newSubscriptionWithPostman(t, comp, pm, nil)

		ctx := context.WithValue(t.Context(), handlerCtxKey{}, "from-the-handler")
		require.NoError(t, comp.deliver(ctx, "topic0", []byte(`{"orderId":"1"}`)))

		assert.Equal(t, "from-the-handler", delivered.Load())
	})

	t.Run("the delivery context carries the handler context's deadline", func(t *testing.T) {
		comp := newPausablePubSub()

		var (
			hasDeadline atomic.Bool
			deadline    atomic.Value
		)

		pm := fakepostman.New().WithDeliverFn(func(ctx context.Context, _ *runtimePubsub.SubscribedMessage) error {
			dl, ok := ctx.Deadline()
			hasDeadline.Store(ok)
			deadline.Store(dl)

			return nil
		})

		newSubscriptionWithPostman(t, comp, pm, nil)

		// Far enough out that it cannot fire during the test, and far enough
		// from any policy timeout to be recognisable.
		want := time.Now().Add(time.Hour)
		ctx, cancel := context.WithDeadline(t.Context(), want)
		defer cancel()

		require.NoError(t, comp.deliver(ctx, "topic0", []byte(`{"orderId":"1"}`)))

		require.True(t, hasDeadline.Load(), "the delivery must inherit the handler context's deadline")
		assert.WithinDuration(t, want, deadline.Load().(time.Time), time.Minute)
	})

	t.Run("cancelling the handler context cancels an in-flight delivery", func(t *testing.T) {
		comp := newPausablePubSub()

		var (
			cancelled atomic.Bool
			entered   = make(chan struct{})
		)

		pm := fakepostman.New().WithDeliverFn(func(ctx context.Context, _ *runtimePubsub.SubscribedMessage) error {
			close(entered)

			select {
			case <-ctx.Done():
				cancelled.Store(true)
			case <-time.After(20 * time.Second):
				// The delivery context was never cancelled. Give up rather
				// than hang the suite; the assertions below report it.
			}

			return ctx.Err()
		})

		newSubscriptionWithPostman(t, comp, pm, nil)

		ctx, cancel := context.WithCancel(t.Context())

		done := make(chan error, 1)

		go func() { done <- comp.deliver(ctx, "topic0", []byte(`{"orderId":"1"}`)) }()

		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the message was never delivered to the postman")
		}

		cancel()

		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("the handler did not return after its context was cancelled")
		}

		assert.True(t, cancelled.Load(), "the in-flight delivery must observe the handler context's cancellation")
	})

	t.Run("cancelling the handler context stops the resiliency runner retrying", func(t *testing.T) {
		comp := newPausablePubSub()

		// Enough retries that a runner ignoring the handler context would
		// still be going long after the test cancels it.
		const maxRetries = 200

		res := createResPolicyProvider(resiliencyV1alpha.CircuitBreaker{}, "1m", resiliencyV1alpha.Retry{
			Policy:      "constant",
			Duration:    "20ms",
			MaxRetries:  new(maxRetries),
			MaxInterval: "20ms",
		})

		var attempts atomic.Int64

		pm := fakepostman.New().WithDeliverFn(func(context.Context, *runtimePubsub.SubscribedMessage) error {
			attempts.Add(1)

			return rterrors.NewRetriable(assert.AnError)
		})

		newSubscriptionWithPostman(t, comp, pm, res)

		ctx, cancel := context.WithCancel(t.Context())

		done := make(chan error, 1)

		go func() { done <- comp.deliver(ctx, "topic0", []byte(`{"orderId":"1"}`)) }()

		// Let the runner get properly into its retry loop first, so that a
		// runner which ignores the cancellation below has somewhere to go.
		require.Eventually(t, func() bool {
			return attempts.Load() >= 3
		}, 10*time.Second, 10*time.Millisecond, "the runner never retried the delivery")

		cancel()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the runner kept retrying after its context was cancelled")
		}

		// Give a runner that ignored the cancellation a chance to show itself.
		settled := attempts.Load()
		time.Sleep(500 * time.Millisecond)

		assert.Equal(t, settled, attempts.Load(), "no delivery may be attempted after the handler context is cancelled")
		assert.Less(t, settled, int64(maxRetries), "the runner must not exhaust its retries after being cancelled")
	})
}
