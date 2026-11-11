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
	"google.golang.org/protobuf/types/known/timestamppb"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func testState(t *testing.T) *wfenginestate.State {
	t.Helper()
	return wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
}

func startedEvent() *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{Name: "TestWorkflow"},
		},
	}
}

func taskScheduledEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
		},
	}
}

func taskFailedEvent(scheduledID int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: scheduledID},
		},
	}
}

func Test_unresolvedScheduledTasks(t *testing.T) {
	t.Parallel()

	ids := func(events []*backend.HistoryEvent) []int32 {
		out := make([]int32, 0, len(events))
		for _, e := range events {
			out = append(out, e.GetEventId())
		}
		return out
	}

	t.Run("no scheduled tasks", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("pending task is unresolved", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		assert.Equal(t, []int32{1}, ids(unresolvedScheduledTasks(state, nil)))
	})

	t.Run("resolution in history", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToHistory(taskCompletedEvent(1))
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("resolution pending in inbox", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToInbox(taskCompletedEvent(1))
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("failure counts as resolution", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToHistory(taskFailedEvent(1))
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("mixed: only the unresolved task is returned", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(1))
		state.AddToHistory(taskScheduledEvent(2))
		state.AddToHistory(taskScheduledEvent(3))
		state.AddToHistory(taskCompletedEvent(1))
		state.AddToInbox(taskFailedEvent(3))
		assert.Equal(t, []int32{2}, ids(unresolvedScheduledTasks(state, nil)))
	})

	t.Run("timers and child workflows are not scheduled tasks", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(&protos.HistoryEvent{
			EventId:   5,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TimerCreated{
				TimerCreated: &protos.TimerCreatedEvent{FireAt: timestamppb.Now()},
			},
		})
		state.AddToHistory(&protos.HistoryEvent{
			EventId:   6,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
				ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{Name: "child"},
			},
		})
		assert.Empty(t, unresolvedScheduledTasks(state, nil))
	})

	t.Run("timer resolution does not resolve a task with the same id", func(t *testing.T) {
		state := testState(t)
		state.AddToHistory(startedEvent())
		state.AddToHistory(taskScheduledEvent(4))
		state.AddToHistory(&protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{TimerId: 4},
			},
		})
		assert.Equal(t, []int32{4}, ids(unresolvedScheduledTasks(state, nil)))
	})
}

// A janitor re-dispatch that lands after its task resolved and after the
// activity's claim record expired re-executes the body. The record outlives
// the resolution by at least one janitor period, so the call must be bounded
// by it rather than by redispatchCallTimeout.
func Test_redispatchActivities_callBoundedByJanitorPeriod(t *testing.T) {
	period := janitorPeriod
	janitorPeriod = func() time.Duration { return time.Second }
	t.Cleanup(func() { janitorPeriod = period })
	require.Less(t, janitorPeriod(), redispatchCallTimeout, "the test needs a janitor period below the call timeout to tell them apart")

	h := newNotifyHarness(t, []*backend.HistoryEvent{startedEvent(), taskScheduledEvent(0), taskScheduledEvent(1)}, nil, false, nil)
	state, _, err := h.orch.loadInternalState(t.Context())
	require.NoError(t, err)
	unresolved := unresolvedScheduledTasks(state, nil)
	require.Len(t, unresolved, 2)

	fired := time.Now()
	h.orch.redispatchActivities(t.Context(), state, unresolved)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		h.lock.Lock()
		defer h.lock.Unlock()
		assert.Len(c, h.deadlines, 2)
	}, 5*time.Second, 10*time.Millisecond)
	h.lock.Lock()
	defer h.lock.Unlock()
	// The calls share one deadline: every one must end within one janitor
	// period of the fire, not of its own start.
	assert.Equal(t, h.deadlines[0], h.deadlines[1])
	assert.WithinDuration(t, fired.Add(janitorPeriod()), h.deadlines[1], 100*time.Millisecond)
}

func Test_redispatchActivities_callsDoNotWaitForEachOther(t *testing.T) {
	period := janitorPeriod
	janitorPeriod = func() time.Duration { return 200 * time.Millisecond }
	t.Cleanup(func() { janitorPeriod = period })

	h := newNotifyHarness(t, []*backend.HistoryEvent{startedEvent(), taskScheduledEvent(0), taskScheduledEvent(1)}, nil, false, nil)
	state, _, err := h.orch.loadInternalState(t.Context())
	require.NoError(t, err)
	unresolved := unresolvedScheduledTasks(state, nil)
	require.Len(t, unresolved, 2)

	// Task 0's target never answers, so its call uses up the pass's deadline.
	var task1Live atomic.Bool
	h.lock.Lock()
	h.onCall = func(ctx context.Context, req *internalv1pb.InternalInvokeRequest) error {
		if strings.HasSuffix(req.GetActor().GetActorId(), "::0::0") {
			<-ctx.Done()
			return ctx.Err()
		}
		if ctx.Err() == nil {
			task1Live.Store(true)
		}
		return nil
	}
	h.lock.Unlock()

	h.orch.redispatchActivities(t.Context(), state, unresolved)
	h.orch.wakeWG.Wait()
	assert.True(t, task1Live.Load(), "task 1 must get a live call in the same pass")
}
