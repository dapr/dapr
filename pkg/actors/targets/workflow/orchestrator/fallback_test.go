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

package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

func TestFallback(t *testing.T) {
	newFactory := func(t *testing.T, fastPath bool, fallback func() bool) *factory {
		t.Helper()
		fact, err := New(t.Context(), Options{
			AppID:             "appID",
			ActivityActorType: "activity",
			WorkflowActorType: "workflow",
			Scheduler: func(context.Context, *backend.WorkflowWorkItem) error {
				return nil
			},
			Actors:   fake.New(),
			FastPath: fastPath,
			Fallback: fallback,
		})
		require.NoError(t, err)
		return fact.(*factory)
	}

	t.Run("with the feature off nothing is a fallback host", func(t *testing.T) {
		f := newFactory(t, true, nil)
		assert.False(t, f.isFallback())
		assert.True(t, f.localFastPath())
	})

	t.Run("a fallback host takes no local fast path, and regains it in place", func(t *testing.T) {
		var fallback atomic.Bool
		fallback.Store(true)
		f := newFactory(t, true, fallback.Load)
		assert.True(t, f.isFallback())
		assert.False(t, f.localFastPath())
		// Turns run only with a local executor, and keep the configured
		// fast path.
		assert.True(t, f.fastPath)

		fallback.Store(false)
		assert.False(t, f.isFallback())
		assert.True(t, f.localFastPath())
	})

	t.Run("without the fast path a fallback host changes nothing for it", func(t *testing.T) {
		f := newFactory(t, false, func() bool { return true })
		assert.False(t, f.localFastPath())
	})

	t.Run("a local wake is not armed on a fallback host", func(t *testing.T) {
		f := newFactory(t, true, func() bool { return true })
		o := f.GetOrCreate("wf1").(*orchestrator)
		o.localDrive("start-1", time.Now())
		assert.False(t, o.driveRunning.Load())
		require.NoError(t, o.Deactivate(t.Context()))
	})
}

func TestFallbackOperation(t *testing.T) {
	event := func(e *protos.HistoryEvent) *backend.HistoryEvent { return e }
	for _, tc := range []struct {
		method string
		ev     *backend.HistoryEvent
		want   string
	}{
		{todo.CreateWorkflowInstanceMethod, nil, diag.CreateWorkflow},
		{todo.PurgeWorkflowStateMethod, nil, ""},
		{todo.RecursivePurgeWorkflowStateMethod, nil, ""},
		{todo.RerunWorkflowInstance, nil, diag.RerunWorkflow},
		// Counted once, as the rerun on the new instance.
		{todo.ForkWorkflowHistory, nil, ""},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{}}}), diag.RaiseEvent},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionTerminated{ExecutionTerminated: &protos.ExecutionTerminatedEvent{}}}), diag.TerminateWorkflow},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}}}), diag.SuspendWorkflow},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionResumed{ExecutionResumed: &protos.ExecutionResumedEvent{}}}), diag.ResumeWorkflow},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_TimerFired{TimerFired: &protos.TimerFiredEvent{}}}), diag.AddEvent},
		// A read is not an operation on the instance.
		{todo.WaitForRuntimeStatus, nil, ""},
	} {
		assert.Equal(t, tc.want, fallbackOperation(tc.method, tc.ev), tc.method)
	}

	t.Run("served operations reach the backend, reads do not", func(t *testing.T) {
		var served int
		fact, err := New(t.Context(), Options{
			AppID:             "appID",
			ActivityActorType: "activity",
			WorkflowActorType: "workflow",
			Scheduler:         func(context.Context, *backend.WorkflowWorkItem) error { return nil },
			Actors:            fake.New(),
			Fallback:          func() bool { return true },
			FallbackServed:    func() { served++ },
		})
		require.NoError(t, err)
		f := fact.(*factory)
		f.fallbackServed(t.Context(), todo.CreateWorkflowInstanceMethod, nil)
		f.fallbackServed(t.Context(), todo.WaitForRuntimeStatus, nil)
		assert.Equal(t, 1, served)
	})
}

// A call that took the fast path before the turn lock, and got the lock only
// after the last worker disconnected, is served as a fallback host serves it:
// on the durable path, with its response marked, so the caller does not wait
// for a state change that comes only once the instance moves.
func TestFallbackAfterLockWait(t *testing.T) {
	const instanceID = "wf1"

	for name, ev := range map[string]*backend.HistoryEvent{
		"suspend": {
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}},
		},
		"activity result": taskCompletedEvent(7),
	} {
		t.Run(name, func(t *testing.T) {
			h := newWakeHarness(t, instanceID, true)
			h.fact.resiliency = resiliency.New(logger.NewLogger("test"))
			var fallback atomic.Bool
			h.fact.fallback = fallback.Load
			h.primeRunning(t, instanceID, 7)

			// A turn holds the lock while a worker is connected.
			unlock, err := h.orch.lock.ContextLock(t.Context())
			require.NoError(t, err)

			data, err := proto.Marshal(ev)
			require.NoError(t, err)
			req := internalsv1pb.NewInternalInvokeRequest(todo.AddWorkflowEventMethod).WithData(data)

			var res *internalsv1pb.InternalInvokeResponse
			done := make(chan error, 1)
			go func() {
				var ierr error
				res, ierr = h.orch.InvokeMethod(t.Context(), req)
				done <- ierr
			}()
			require.Eventually(t, func() bool { return h.orch.lockWaiters.Load() == 1 }, 5*time.Second, time.Millisecond)

			// The last worker disconnects before the call gets the lock.
			fallback.Store(true)
			unlock()

			select {
			case err = <-done:
			case <-time.After(10 * time.Second):
				require.Fail(t, "the call must not wait for a folding turn on a fallback host")
			}
			require.NoError(t, err)
			assert.True(t, common.IsFallback(res), "the response must be marked as served by a fallback host")
			assert.Empty(t, h.orch.foldPending, "a fallback host must not hold the event for a local turn")

			ops := h.snapshotOps()
			assert.Contains(t, ops, "save", "the event must be persisted to the inbox")
			for _, op := range ops {
				assert.NotContains(t, op, "callReminder:", "a fallback host must not drive a local turn")
			}
		})
	}
}
