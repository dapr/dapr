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

package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const haltTestActorType = "dapr.internal.default.test.executor"

// newHaltTestFactory builds an executor factory whose actor router calls back
// into the same factory, so a forward after a halt reaches the fresh actor
// that stands in for the key's new owner.
func newHaltTestFactory(t *testing.T, p *pending.Pending) targets.Factory {
	t.Helper()

	var f targets.Factory
	routerFake := routerfake.New().
		WithCallFn(func(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			return f.GetOrCreate(req.GetActor().GetActorId()).InvokeMethod(ctx, req)
		})
	actorsFake := actorsfake.New().
		WithRouter(func(context.Context) (router.Interface, error) {
			return routerFake, nil
		})

	var err error
	f, err = New(t.Context(), Options{
		Actors:    actorsFake,
		ActorType: haltTestActorType,
		Pending:   p,
	})
	require.NoError(t, err)
	return f
}

// Test_haltForwardsParkedCompletion covers a completion that parks on an
// executor actor before its watcher attaches, when a placement rebalance then
// moves the actor's key to another host. The watcher attaches on the new
// owner, so the completion must follow the key there.
func Test_haltForwardsParkedCompletion(t *testing.T) {
	t.Parallel()

	f := newHaltTestFactory(t, pending.New())
	actorType := haltTestActorType
	var err error

	// No waiter is registered on this host, so the completion parks. A
	// workflow-type key keeps complete() off the sibling forward.
	_, err = f.GetOrCreate("abc").InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("genuine")))
	require.NoError(t, err)

	// The rebalance moves the key before the watcher attaches.
	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))

	// The watcher attaches to the key's new owner: here, a fresh actor from
	// the same factory, which the forward also reaches through the router.
	got := make(chan []byte, 1)
	go func() {
		_ = f.GetOrCreate("abc").InvokeStream(t.Context(),
			internalsv1pb.NewInternalInvokeRequest(MethodWatchComplete).
				WithActor(actorType, "abc").
				WithContentType(invokev1.ProtobufContentType).
				WithMetadata(map[string][]string{MetadataTaskType: {TaskTypeWorkflow}}),
			func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
				got <- res.GetMessage().GetData().GetValue()
				return true, nil
			})
	}()

	select {
	case data := <-got:
		assert.Equal(t, []byte("genuine"), data)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the completion parked before the rebalance did not reach the new owner")
	}
}

// Test_haltForwardsParkedCancellation covers a cancellation recorded on an
// executor actor with no waiter, when a rebalance moves the key before the
// waiter registers on the new owner. The forward must keep the task type, so
// an activity waiter in the new owner's pending map gets it.
func Test_haltForwardsParkedCancellation(t *testing.T) {
	t.Parallel()

	p := pending.New()
	f := newHaltTestFactory(t, p)
	const key = "abc::0::0"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(),
		internalsv1pb.NewInternalInvokeRequest(MethodCancel).
			WithActor(haltTestActorType, key).
			WithContentType(invokev1.ProtobufContentType).
			WithMetadata(map[string][]string{MetadataTaskType: {TaskTypeActivity}}))
	require.NoError(t, err)

	got := make(chan pending.Result, 1)
	dereg := p.RegisterCallback(PendingKey(TaskTypeActivity, key), func(res pending.Result) {
		got <- res
	})
	t.Cleanup(dereg)

	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))

	select {
	case res := <-got:
		assert.True(t, res.Cancelled)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the cancellation recorded before the rebalance did not reach the waiter")
	}
}

// Test_haltDoesNotForwardTwice covers a completion that reached the actor by
// a forward (a sibling copy, or a completion an earlier halt moved). A halt
// drops it as before: forwarding it again would let a completion that
// nothing consumes follow every rebalance.
func Test_haltDoesNotForwardTwice(t *testing.T) {
	t.Parallel()

	f := newHaltTestFactory(t, pending.New())

	_, err := f.GetOrCreate("abc").InvokeMethod(t.Context(),
		completeReq(TaskTypeWorkflow, []byte("forwarded")).
			WithMetadata(map[string][]string{
				MetadataTaskType:  {TaskTypeWorkflow},
				MetadataForwarded: {forwardedValue},
			}))
	require.NoError(t, err)

	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))

	// Nothing reaches the fresh actor: a claim finds nothing parked.
	assert.Never(t, func() bool {
		res, cerr := f.GetOrCreate("abc").InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		return cerr == nil && res.GetStatus().GetCode() == int32(codes.OK)
	}, time.Second, 50*time.Millisecond)
}

// Test_watchKeepsActorForParkedCompletion covers a completion that parks
// while a watch stream serves the previous one, as when a completer blocked
// on the full channel refills the slot. The stream must not request the
// actor's deactivation, or that completion is dropped before its waiter
// watches again.
func Test_watchKeepsActorForParkedCompletion(t *testing.T) {
	t.Parallel()

	e, f := newClaimTestExecutor(t)

	_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("first")))
	require.NoError(t, err)

	watch := func(onServe func()) []byte {
		var got []byte
		require.NoError(t, e.InvokeStream(t.Context(),
			internalsv1pb.NewInternalInvokeRequest(MethodWatchComplete).
				WithActor(haltTestActorType, "abc").
				WithContentType(invokev1.ProtobufContentType).
				WithMetadata(map[string][]string{MetadataTaskType: {TaskTypeWorkflow}}),
			func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
				got = res.GetMessage().GetData().GetValue()
				if onServe != nil {
					onServe()
				}
				return true, nil
			}))
		return got
	}

	// The second completion parks while the stream serves the first.
	assert.Equal(t, []byte("first"), watch(func() {
		_, cerr := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("second")))
		assert.NoError(t, cerr)
	}))
	assert.Empty(t, f.deactivateCh, "a parked completion must keep the actor alive")

	// The waiter watches again and gets it; the actor then goes idle.
	assert.Equal(t, []byte("second"), watch(nil))
	assert.Len(t, f.deactivateCh, 1)
}
