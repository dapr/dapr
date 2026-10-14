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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets"
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
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

// newQueuedDeactivationHaltFactory builds a factory whose actor router calls
// back into it, but with no deactivation goroutine: a queued idle
// deactivation never runs, so a halt reaches the actor first, as when the
// serial deactivation queue is behind.
func newQueuedDeactivationHaltFactory(p *pending.Pending) *factory {
	var f *factory
	routerFake := routerfake.New().
		WithCallFn(func(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			return f.GetOrCreate(req.GetActor().GetActorId()).InvokeMethod(ctx, req)
		})
	f = &factory{
		actorType: haltTestActorType,
		actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
			return routerFake, nil
		}),
		deactivateCh: make(chan *executor, 10),
		pending:      p,
	}
	return f
}

// watchOnce attaches a watch stream for key on the factory and returns the
// first payload it serves. Like the actor router, it retries on a fresh
// actor when the stream reaches an actor that closed.
func watchOnce(t *testing.T, f targets.Factory, key, taskType string) []byte {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var got []byte
		err := f.GetOrCreate(key).InvokeStream(ctx,
			internalsv1pb.NewInternalInvokeRequest(MethodWatchComplete).
				WithActor(haltTestActorType, key).
				WithContentType(invokev1.ProtobufContentType).
				WithMetadata(map[string][]string{MetadataTaskType: {taskType}}),
			func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
				got = res.GetMessage().GetData().GetValue()
				return true, nil
			})
		if targeterrors.IsClosed(err) {
			continue
		}
		require.NoError(t, err)
		return got
	}
}

// parkedOn reports whether the actor for key exists and holds a parked
// completion.
func parkedOn(f *factory, key string) bool {
	a, ok := f.table.Load(key)
	return ok && len(a.(*executor).completeCh) == 1
}

// Test_haltDoesNotForwardDeliveredCopy covers the copy that a delivery parks
// for stale watch streams. A halt must not hand it to the next turn's waiter
// on the new owner: that result was already delivered.
func Test_haltDoesNotForwardDeliveredCopy(t *testing.T) {
	t.Parallel()

	p := pending.New()
	f := newQueuedDeactivationHaltFactory(p)
	const key = "wf1"

	turn1 := make(chan pending.Result, 1)
	dereg1 := p.RegisterCallback(PendingKey(TaskTypeWorkflow, key), func(r pending.Result) { turn1 <- r })
	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("turn1")))
	require.NoError(t, err)
	require.Equal(t, []byte("turn1"), (<-turn1).Data)
	dereg1()

	turn2 := make(chan pending.Result, 1)
	t.Cleanup(p.RegisterCallback(PendingKey(TaskTypeWorkflow, key), func(r pending.Result) { turn2 <- r }))

	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))

	select {
	case r := <-turn2:
		assert.Failf(t, "turn 2's waiter received turn 1's already-delivered result", "data=%q", r.Data)
	case <-time.After(time.Second):
	}
}

// Test_haltDoesNotForwardServedCancellation covers a cancellation that a
// claim already served. A halt must not hand it to the retried attempt's
// waiter on the new owner.
func Test_haltDoesNotForwardServedCancellation(t *testing.T) {
	t.Parallel()

	p := pending.New()
	f := newQueuedDeactivationHaltFactory(p)
	const key = "wf2"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(), cancelReq(TaskTypeWorkflow))
	require.NoError(t, err)
	res, err := f.GetOrCreate(key).InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
	require.NoError(t, err)
	require.Equal(t, int32(codes.Aborted), res.GetStatus().GetCode())

	retried := make(chan pending.Result, 1)
	t.Cleanup(p.RegisterCallback(PendingKey(TaskTypeWorkflow, key), func(r pending.Result) { retried <- r }))

	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))

	select {
	case <-retried:
		assert.Fail(t, "the retried attempt's waiter got the already-served cancellation")
	case <-time.After(time.Second):
	}
}

// Test_haltDoesNotForwardCancellationServedToWatch is the watch-stream
// variant of Test_haltDoesNotForwardServedCancellation.
func Test_haltDoesNotForwardCancellationServedToWatch(t *testing.T) {
	t.Parallel()

	p := pending.New()
	f := newQueuedDeactivationHaltFactory(p)
	const key = "wf4"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(), cancelReq(TaskTypeWorkflow))
	require.NoError(t, err)
	var code int32
	require.NoError(t, f.GetOrCreate(key).InvokeStream(t.Context(),
		internalsv1pb.NewInternalInvokeRequest(MethodWatchComplete).
			WithActor(haltTestActorType, key).
			WithContentType(invokev1.ProtobufContentType).
			WithMetadata(map[string][]string{MetadataTaskType: {TaskTypeWorkflow}}),
		func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
			code = res.GetStatus().GetCode()
			return true, nil
		}))
	require.Equal(t, int32(codes.Aborted), code)

	retried := make(chan pending.Result, 1)
	t.Cleanup(p.RegisterCallback(PendingKey(TaskTypeWorkflow, key), func(r pending.Result) { retried <- r }))

	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))

	select {
	case <-retried:
		assert.Fail(t, "the retried attempt's waiter got the cancellation already served to a watch stream")
	case <-time.After(time.Second):
	}
}

// Test_haltDoesNotForwardForwardedCancellation is the cancellation
// counterpart of Test_haltDoesNotForwardTwice: a forwarded cancellation with
// no handoff count never moves again.
func Test_haltDoesNotForwardForwardedCancellation(t *testing.T) {
	t.Parallel()

	p := pending.New()
	f := newQueuedDeactivationHaltFactory(p)
	const key = "wf3"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(),
		cancelReq(TaskTypeWorkflow).WithMetadata(map[string][]string{
			MetadataTaskType:  {TaskTypeWorkflow},
			MetadataForwarded: {forwardedValue},
		}))
	require.NoError(t, err)

	got := make(chan pending.Result, 1)
	t.Cleanup(p.RegisterCallback(PendingKey(TaskTypeWorkflow, key), func(r pending.Result) { got <- r }))

	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))

	select {
	case <-got:
		assert.Fail(t, "a forwarded cancellation was forwarded again")
	case <-time.After(time.Second):
	}
}

// Test_haltHandsOffAcrossConsecutiveRebalances covers a key that moves twice
// before its watcher attaches, as when placement rounds run back to back
// during worker churn. The completion follows the key both times.
func Test_haltHandsOffAcrossConsecutiveRebalances(t *testing.T) {
	t.Parallel()

	f := newQueuedDeactivationHaltFactory(pending.New())
	const key = "wf5"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("genuine")))
	require.NoError(t, err)

	for round := range 2 {
		require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))
		require.Eventually(t, func() bool { return parkedOn(f, key) }, 5*time.Second, 10*time.Millisecond,
			"round %d: the completion did not reach the key's new owner", round)
	}

	assert.Equal(t, []byte("genuine"), watchOnce(t, f, key, TaskTypeWorkflow))
}

// Test_handoffBudget covers a completion that nothing consumes. It moves at
// most maxHandoffs times, then a retirement drops it.
func Test_handoffBudget(t *testing.T) {
	t.Parallel()

	f := newQueuedDeactivationHaltFactory(pending.New())
	const key = "wf6"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("orphan")))
	require.NoError(t, err)

	for round := range maxHandoffs {
		require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))
		require.Eventually(t, func() bool { return parkedOn(f, key) }, 5*time.Second, 10*time.Millisecond,
			"handoff %d did not arrive", round+1)
	}

	require.NoError(t, f.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return false }))
	assert.Never(t, func() bool { return parkedOn(f, key) }, time.Second, 50*time.Millisecond,
		"a completion moved more than maxHandoffs times")
}

// Test_idleRetirementHandsOffParkedCompletion covers a completion that parks
// while a watch stream serves the previous one. The stream ends, and the idle
// actor retires before its waiter watches again. The completion moves to a
// fresh actor for the same key, where the next watch stream finds it.
func Test_idleRetirementHandsOffParkedCompletion(t *testing.T) {
	t.Parallel()

	f := newHaltTestFactory(t, pending.New())
	const key = "wf7"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("first")))
	require.NoError(t, err)

	first := f.GetOrCreate(key)
	var got []byte
	require.NoError(t, first.InvokeStream(t.Context(),
		internalsv1pb.NewInternalInvokeRequest(MethodWatchComplete).
			WithActor(haltTestActorType, key).
			WithContentType(invokev1.ProtobufContentType).
			WithMetadata(map[string][]string{MetadataTaskType: {TaskTypeWorkflow}}),
		func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
			got = res.GetMessage().GetData().GetValue()
			// The second completion parks while the stream serves the first.
			_, cerr := first.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("second")))
			assert.NoError(t, cerr)
			return true, nil
		}))
	require.Equal(t, []byte("first"), got)

	// The stream requested the actor's deactivation, and the factory's
	// deactivation goroutine retires it.
	require.Eventually(t, func() bool {
		a, ok := f.(*factory).table.Load(key)
		return ok && a != first
	}, 5*time.Second, 10*time.Millisecond, "the idle actor did not retire and hand off its completion")

	assert.Equal(t, []byte("second"), watchOnce(t, f, key, TaskTypeWorkflow))
}

// Test_haltAllHandOffRetriesUntilKeyMoves covers HaltAll, which runs after the
// type left this host's table but before placement stops resolving the key
// here. The handoff gets ErrCreatingActor until the key moves, and retries.
func Test_haltAllHandOffRetriesUntilKeyMoves(t *testing.T) {
	t.Parallel()

	var (
		f        *factory
		attempts atomic.Int32
	)
	routerFake := routerfake.New().
		WithCallFn(func(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			if attempts.Add(1) <= 3 {
				return nil, fmt.Errorf("%w: actor type %s not registered", actorerrors.ErrCreatingActor, haltTestActorType)
			}
			return f.GetOrCreate(req.GetActor().GetActorId()).InvokeMethod(ctx, req)
		})
	f = &factory{
		actorType: haltTestActorType,
		actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
			return routerFake, nil
		}),
		deactivateCh: make(chan *executor, 10),
		pending:      pending.New(),
	}
	const key = "wf8"

	_, err := f.GetOrCreate(key).InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("genuine")))
	require.NoError(t, err)

	require.NoError(t, f.HaltAll(t.Context()))
	require.Eventually(t, func() bool { return parkedOn(f, key) }, 5*time.Second, 10*time.Millisecond,
		"the handoff did not retry until the key moved")
	assert.GreaterOrEqual(t, attempts.Load(), int32(4))
}

// Test_cancelAfterRetirementIsRetried covers a cancellation that reaches an
// actor after a retirement closed it and took its snapshot. It must not be
// recorded on the closed actor, where nothing hands it off: the caller gets a
// closed error and retries on a fresh actor.
func Test_cancelAfterRetirementIsRetried(t *testing.T) {
	t.Parallel()

	f := newQueuedDeactivationHaltFactory(pending.New())
	const key = "wf9"

	e := f.GetOrCreate(key).(*executor)
	parked, canc := e.deactivate()
	require.Empty(t, parked)
	require.Empty(t, canc.taskType)

	_, err := e.InvokeMethod(t.Context(), cancelReq(TaskTypeWorkflow))
	require.True(t, targeterrors.IsClosed(err), "err: %v", err)

	// The retry reaches a fresh actor and records it there.
	_, err = f.GetOrCreate(key).InvokeMethod(t.Context(), cancelReq(TaskTypeWorkflow))
	require.NoError(t, err)
	res, err := f.GetOrCreate(key).InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
	require.NoError(t, err)
	assert.Equal(t, int32(codes.Aborted), res.GetStatus().GetCode())
}
