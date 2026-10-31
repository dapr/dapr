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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/actors/fake"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/logger"
)

func TestFallback(t *testing.T) {
	newOrchestrator := func(t *testing.T, fastPath bool, fallback func() bool) *orchestrator {
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
		return &orchestrator{factory: fact.(*factory)}
	}

	t.Run("with the feature off nothing is a fallback host", func(t *testing.T) {
		o := newOrchestrator(t, true, nil)
		assert.False(t, o.isFallback())
		assert.True(t, o.localFastPath())
	})

	t.Run("a fallback host takes no local fast path, and regains it in place", func(t *testing.T) {
		var fallback atomic.Bool
		fallback.Store(true)
		o := newOrchestrator(t, true, fallback.Load)
		assert.True(t, o.isFallback())
		assert.False(t, o.localFastPath())
		// Turns run only with a local executor, and keep the configured
		// fast path.
		assert.True(t, o.fastPath)

		fallback.Store(false)
		assert.False(t, o.isFallback())
		assert.True(t, o.localFastPath())
	})

	t.Run("without the fast path a fallback host changes nothing for it", func(t *testing.T) {
		o := newOrchestrator(t, false, func() bool { return true })
		assert.False(t, o.localFastPath())
	})

	t.Run("a local wake is not armed on a fallback host", func(t *testing.T) {
		o := newOrchestrator(t, true, func() bool { return true })
		o = o.GetOrCreate("wf1").(*orchestrator)
		o.localDrive("start-1", time.Now())
		assert.False(t, o.driveRunning.Load())
		require.NoError(t, o.Deactivate(t.Context()))
	})

	t.Run("an invoked method sees the fallback state read at its start", func(t *testing.T) {
		var fallback atomic.Bool
		fallback.Store(true)
		o := newOrchestrator(t, true, fallback.Load)
		pinned, unpin := o.pinFallback()
		assert.True(t, pinned)

		// A worker connects while the method runs.
		fallback.Store(false)
		assert.True(t, o.isFallback())
		assert.False(t, o.localFastPath())

		unpin()
		assert.False(t, o.isFallback())
		assert.True(t, o.localFastPath())
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
		// An event from the workflow's own progress is not an operation a
		// user made.
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{}}}), ""},
		{todo.AddWorkflowEventMethod, event(&protos.HistoryEvent{EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{}}}), ""},
		// A read is not an operation on the instance.
		{todo.WaitForRuntimeStatus, nil, ""},
	} {
		assert.Equal(t, tc.want, fallbackOperation(tc.method, tc.ev), tc.method)
	}
}

// A worker that connects while a fallback host serves a call does not change
// the call's path half way: the event is saved with a durable reminder and no
// local drive, and the response is marked so the caller does not wait for a
// state change that comes only once the reminder fires.
func TestFallbackWorkerConnectsMidCall(t *testing.T) {
	const instanceID = "wf1"

	h := newWakeHarness(t, instanceID, true)
	h.fact.resiliency = resiliency.New(logger.NewLogger("test"))
	// Only the first read sees a fallback host: the worker connects right
	// after it.
	var reads atomic.Int32
	h.fact.fallback = func() bool { return reads.Add(1) == 1 }
	h.primeRunning(t, instanceID, 7)

	data, err := proto.Marshal(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}},
	})
	require.NoError(t, err)
	req := internalsv1pb.NewInternalInvokeRequest(todo.AddWorkflowEventMethod).WithData(data)

	res, err := h.orch.handleInvoke(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(1), reads.Load(), "the call must read the fallback state once")
	assert.True(t, common.IsFallback(res), "the response must be marked as served by a fallback host")
	assert.False(t, h.orch.driveRunning.Load(), "a call served as a fallback host must not drive a local turn")

	ops := h.snapshotOps()
	assert.Contains(t, ops, "save")
	var reminder bool
	for _, op := range ops {
		reminder = reminder || strings.HasPrefix(op, "create:new-event-")
	}
	assert.True(t, reminder, "the event must get a durable wake-up reminder: %v", ops)
	require.NoError(t, h.orch.Deactivate(t.Context()))
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

// A call on the fast path whose last worker disconnects while it admits the
// event keeps the path it started on, as an invoked method does: the event
// is driven locally, not left behind a durable reminder with an unmarked
// response, which the caller would wait on until a worker connects.
func TestFallbackWorkerDisconnectsMidFold(t *testing.T) {
	const instanceID = "wf1"

	h := newWakeHarness(t, instanceID, true)
	h.fact.resiliency = resiliency.New(logger.NewLogger("test"))
	// InvokeMethod reads the state to choose the fold path, and the fold
	// path again under the lock: the last worker disconnects right after.
	var reads atomic.Int32
	h.fact.fallback = func() bool { return reads.Add(1) > 2 }
	h.primeRunning(t, instanceID, 7)

	data, err := proto.Marshal(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}},
	})
	require.NoError(t, err)
	req := internalsv1pb.NewInternalInvokeRequest(todo.AddWorkflowEventMethod).WithData(data)

	res, err := h.orch.InvokeMethod(t.Context(), req)
	require.NoError(t, err)
	assert.False(t, common.IsFallback(res), "a call that found a worker must not be marked")
	assert.Nil(t, h.orch.pinnedFallback.Load(), "the state must be released once the event is admitted")

	ops := h.snapshotOps()
	assert.Contains(t, ops, "save")
	for _, op := range ops {
		// The janitor shares the prefix, and backs the local path.
		durable := strings.HasPrefix(op, "create:new-event-") && op != "create:"+janitorReminderName
		assert.False(t, durable, "the event must keep the local path it started on: %v", ops)
	}
	require.NoError(t, h.orch.Deactivate(t.Context()))
}

// A rerun's response is marked by whether the new instance's daprd is a
// fallback host, not the source's: the caller waits on the new instance.
func TestFallbackForkFollowsNewInstance(t *testing.T) {
	const instanceID = "wf1"

	for name, tc := range map[string]struct {
		source, destination bool
	}{
		"source has a worker, new instance staged": {false, true},
		"source staged, new instance has a worker": {true, false},
	} {
		t.Run(name, func(t *testing.T) {
			h := newWakeHarness(t, instanceID, true)
			h.fact.resiliency = resiliency.New(logger.NewLogger("test"))
			h.fact.fallback = func() bool { return tc.source }
			h.fact.router = routerfake.New().WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
				assert.Equal(t, todo.RerunWorkflowInstance, req.GetMessage().GetMethod())
				res := &internalsv1pb.InternalInvokeResponse{Status: &internalsv1pb.Status{Code: 200}}
				if tc.destination {
					common.SetFallback(res)
				}
				return res, nil
			})
			h.primeRunning(t, instanceID, 0)
			h.orch.state.AddToHistory(taskCompletedEvent(0))
			h.orch.state.AddToHistory(&protos.HistoryEvent{
				EventId:   -1,
				Timestamp: timestamppb.Now(),
				EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				}},
			})
			h.orch.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, h.orch.state.History)
			h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, h.orch.state.History[0].GetExecutionStarted())

			data, err := proto.Marshal(&backend.RerunWorkflowFromEventRequest{
				SourceInstanceID: instanceID,
				NewInstanceID:    new("wf2"),
				EventID:          0,
			})
			require.NoError(t, err)
			req := internalsv1pb.NewInternalInvokeRequest(todo.ForkWorkflowHistory).WithData(data)

			res, err := h.orch.handleInvoke(t.Context(), req)
			require.NoError(t, err)
			assert.Equal(t, tc.destination, common.IsFallback(res))
		})
	}
}
