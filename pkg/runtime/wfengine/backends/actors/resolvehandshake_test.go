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

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

// fakePendingBackend hands the registered completion callback back to the
// test so the resolve/held ordering can be observed at the exact delivery
// instant.
type fakePendingBackend struct {
	PendingTasksBackend
	cb func(*protos.ActivityResponse, error)
}

func (f *fakePendingBackend) OnActivityCompletion(_ *protos.ActivityRequest, cb func(*protos.ActivityResponse, error)) func() {
	f.cb = cb
	return func() {}
}

func activityRequest(iid string, taskID int32) *protos.ActivityRequest {
	return &protos.ActivityRequest{
		WorkflowInstance: &protos.WorkflowInstance{InstanceId: iid},
		TaskId:           taskID,
	}
}

// Test_activityCompletionHandshake pins the stale-claim handshake contract:
// the registered resolver runs BEFORE the waiter's held registration is
// released, so at no observable instant is a completed execution both
// not-held and not-resolving.
func Test_activityCompletionHandshake(t *testing.T) {
	t.Parallel()

	t.Run("resolver runs while the registration is still held", func(t *testing.T) {
		t.Parallel()
		fake := &fakePendingBackend{}
		abe := &Actors{pendingTasksBackend: fake, activityExecs: newActivityExecutions()}

		heldAtResolve := make(chan bool, 1)
		unregister := abe.RegisterActivityResolver("wf1", 3, func() {
			heldAtResolve <- abe.ActivityExecutionHeld("wf1", 3)
		})
		defer unregister()

		dereg := abe.OnActivityCompletion(activityRequest("wf1", 3), func(*protos.ActivityResponse, error) {})
		require.True(t, abe.ActivityExecutionHeld("wf1", 3), "an armed registration must read as held")

		fake.cb(&protos.ActivityResponse{TaskId: 3}, nil)

		select {
		case held := <-heldAtResolve:
			assert.True(t, held, "the resolver must observe the registration still held: releasing first reopens the eviction window")
		default:
			require.Fail(t, "the resolver was never invoked on successful completion")
		}
		assert.True(t, abe.ActivityExecutionHeld("wf1", 3), "held must survive the delivery: the registration stays armed until settlement")
		dereg()
		assert.False(t, abe.ActivityExecutionHeld("wf1", 3), "the registration must be released at deregistration")
	})

	t.Run("error paths do not resolve, keeping the execution evictable", func(t *testing.T) {
		t.Parallel()
		fake := &fakePendingBackend{}
		abe := &Actors{pendingTasksBackend: fake, activityExecs: newActivityExecutions()}

		resolved := false
		unregister := abe.RegisterActivityResolver("wf1", 4, func() { resolved = true })
		defer unregister()

		dereg := abe.OnActivityCompletion(activityRequest("wf1", 4), func(*protos.ActivityResponse, error) {})
		fake.cb(nil, api.ErrTaskCancelled)
		assert.False(t, resolved, "a cancelled or lost work item must not enter resolve: it must stay evictable")
		dereg()
		assert.False(t, abe.ActivityExecutionHeld("wf1", 4))
	})

	t.Run("unregistered or foreign keys resolve as a no-op", func(t *testing.T) {
		t.Parallel()
		a := newActivityExecutions()
		a.resolve(activityExecutionKey("wf9", 9))

		calls := 0
		unregister := a.registerResolver("wf1", 5, func() { calls++ })
		a.resolve(activityExecutionKey("wf2", 5))
		a.resolve(activityExecutionKey("wf1", 6))
		assert.Zero(t, calls, "only the exact execution key may resolve")

		a.resolve(activityExecutionKey("wf1", 5))
		assert.Equal(t, 1, calls)

		unregister()
		a.resolve(activityExecutionKey("wf1", 5))
		assert.Equal(t, 1, calls, "resolve after unregister must be a no-op")
		unregister()
	})

	t.Run("re-registration overwrites for a fresh owner", func(t *testing.T) {
		t.Parallel()
		a := newActivityExecutions()
		var first, second int
		a.registerResolver("wf1", 7, func() { first++ })
		unregister2 := a.registerResolver("wf1", 7, func() { second++ })
		a.resolve(activityExecutionKey("wf1", 7))
		assert.Zero(t, first, "an evicted owner's stale resolver must not fire")
		assert.Equal(t, 1, second)
		unregister2()
	})

	t.Run("a stale owner's unregister must not delete the fresh owner's resolver", func(t *testing.T) {
		t.Parallel()
		a := newActivityExecutions()
		var fresh int
		staleUnregister := a.registerResolver("wf1", 8, func() {})
		a.registerResolver("wf1", 8, func() { fresh++ })

		// The evicted owner settles late and unregisters: the fresh owner's
		// registration must survive, or the eviction window reopens for the
		// healthy execution.
		staleUnregister()
		a.resolve(activityExecutionKey("wf1", 8))
		assert.Equal(t, 1, fresh, "the fresh registration must survive a stale unregister")
	})

	t.Run("delivery with no resolver registered is a no-op resolve", func(t *testing.T) {
		t.Parallel()
		fake := &fakePendingBackend{}
		abe := &Actors{pendingTasksBackend: fake, activityExecs: newActivityExecutions()}
		called := false
		var gotErr error
		dereg := abe.OnActivityCompletion(activityRequest("wf1", 8), func(_ *protos.ActivityResponse, err error) {
			called = true
			gotErr = err
		})
		defer dereg()
		fake.cb(&protos.ActivityResponse{TaskId: 8}, nil)
		require.True(t, called, "completion callback was not invoked")
		require.NoError(t, gotErr)
	})
}
