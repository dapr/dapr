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

package activity

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func newExecHarness() (*factory, chan *backend.ActivityWorkItem) {
	scheduled := make(chan *backend.ActivityWorkItem, 2)
	f := &factory{
		appID:             "testapp",
		actorType:         "dapr.internal.default.testapp.activity",
		workflowActorType: "dapr.internal.default.testapp.workflow",
		router:            routerfake.New(),
		signing:           &signing.Signing{Namespace: "default"},
		scheduler: func(_ context.Context, wi *backend.ActivityWorkItem) error {
			scheduled <- wi
			return nil
		},
	}
	return f, scheduled
}

func Test_executeActivity_lockFreeDuringExecution(t *testing.T) {
	t.Parallel()
	f, scheduled := newExecHarness()

	a := f.GetOrCreate("wf::3").(*activity)

	ownerErr := make(chan error, 1)
	go func() {
		ownerErr <- a.executeActivity(t.Context(), activityReminderName, testInvocation(), false)
	}()

	var wi *backend.ActivityWorkItem
	select {
	case wi = <-scheduled:
	case <-time.After(time.Second * 5):
		t.Fatal("timed out waiting for the WorkItem dispatch")
	}

	// The owner is parked on the SDK callback (the app roundtrip). The actor
	// lock must be free: Execute dispatches and duplicate reminder fires must
	// not queue behind the execution.
	unlock, err := a.lock.ContextLock(t.Context())
	require.NoError(t, err, "the actor lock must be free while the app executes")
	unlock()

	// A duplicate locked fire during the unlocked execution joins as a
	// follower via the inflight entry and must not dispatch a second
	// WorkItem.
	followerErr := make(chan error, 1)
	go func() {
		followerErr <- a.executeActivity(t.Context(), activityReminderName, testInvocation(), false)
	}()

	wi.Result = &protos.HistoryEvent{
		EventId: -1,
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 3},
		},
	}
	callback, ok := wi.Properties[todo.CallbackChannelProperty].(chan bool)
	require.True(t, ok)
	callback <- true

	select {
	case err := <-ownerErr:
		require.NoError(t, err)
	case <-time.After(time.Second * 5):
		t.Fatal("timed out waiting for the owner to finish")
	}
	select {
	case err := <-followerErr:
		require.NoError(t, err)
	case <-time.After(time.Second * 5):
		t.Fatal("timed out waiting for the follower to finish")
	}

	select {
	case <-scheduled:
		t.Fatal("the duplicate fire must not dispatch a second WorkItem")
	default:
	}
}

func Test_claim(t *testing.T) {
	t.Parallel()
	f, _ := newExecHarness()

	a := f.GetOrCreate("wf::3").(*activity)

	unlock, err := a.lock.ContextLock(t.Context())
	require.NoError(t, err)

	// A skipLock claim must not touch the actor lock.
	call, owner, err := a.claim(t.Context(), "k1", "wf", 3, true)
	require.NoError(t, err)
	assert.True(t, owner)
	a.inflight.Release("k1", call)

	// A locked claim parks on the held lock and surfaces ctx cancellation.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = a.claim(ctx, "k2", "wf", 3, false)
	require.ErrorIs(t, err, context.Canceled)

	unlock()
	call, owner, err = a.claim(t.Context(), "k2", "wf", 3, false)
	require.NoError(t, err)
	assert.True(t, owner)

	// A second claim for the same key is a follower on the same call.
	call2, owner2, err := a.claim(t.Context(), "k2", "wf", 3, false)
	require.NoError(t, err)
	assert.False(t, owner2)
	assert.Same(t, call, call2)
	a.inflight.Release("k2", call)
}

// Test_claimStaleEviction covers the janitor-livelock rescue: a claim held by
// a dead execution (unsettled, past the grace, no engine-held work item) is
// evicted so the arrival re-executes as a fresh owner, while live, young, or
// settled claims are followed as before.
func Test_claimStaleEviction(t *testing.T) {
	t.Parallel()

	newHarness := func(held func(string, int32) bool, grace time.Duration) *activity {
		f, _ := newExecHarness()
		f.executionHeld = held
		f.staleClaimAfter = grace
		return f.GetOrCreate("wf::3").(*activity)
	}

	t.Run("dead claim is evicted and ownership reclaimed", func(t *testing.T) {
		t.Parallel()
		a := newHarness(func(string, int32) bool { return false }, time.Millisecond)

		stale, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		require.True(t, owner)
		time.Sleep(5 * time.Millisecond)

		fresh, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		assert.True(t, owner, "the rescue must become a fresh owner")
		assert.NotSame(t, stale, fresh)

		// The evicted call is settled with the recoverable eviction error so
		// parked followers unblock into their retry chains.
		select {
		case <-stale.Done():
		default:
			t.Fatal("the evicted call must be settled")
		}
		require.ErrorIs(t, stale.Err(), errStaleClaimEvicted)
	})

	t.Run("engine-held claim is never evicted", func(t *testing.T) {
		t.Parallel()
		a := newHarness(func(string, int32) bool { return true }, time.Millisecond)

		call, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		require.True(t, owner)
		time.Sleep(5 * time.Millisecond)

		follower, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		assert.False(t, owner, "a live long-running execution must be followed, not evicted")
		assert.Same(t, call, follower)
	})

	t.Run("young claim is never evicted", func(t *testing.T) {
		t.Parallel()
		a := newHarness(func(string, int32) bool { return false }, time.Hour)

		call, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		require.True(t, owner)

		follower, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		assert.False(t, owner)
		assert.Same(t, call, follower)
	})

	t.Run("settled claim keeps its cached outcome", func(t *testing.T) {
		t.Parallel()
		a := newHarness(func(string, int32) bool { return false }, time.Millisecond)

		call, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		require.True(t, owner)
		call.Finish(nil)
		time.Sleep(5 * time.Millisecond)

		follower, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		assert.False(t, owner, "a cached outcome must be followed, never evicted")
		assert.Same(t, call, follower)
		require.NoError(t, follower.Err())
	})

	t.Run("nil oracle disables eviction", func(t *testing.T) {
		t.Parallel()
		a := newHarness(nil, time.Millisecond)

		call, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		require.True(t, owner)
		time.Sleep(5 * time.Millisecond)

		follower, owner, err := a.claim(t.Context(), "k", "wf", 3, false)
		require.NoError(t, err)
		assert.False(t, owner)
		assert.Same(t, call, follower)
	})
}
