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
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	actorreminders "github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

const (
	rejectedTurnTestApp       = "testapp"
	rejectedTurnWorkflowType  = "workflow"
	rejectedTurnActivityType  = "activity"
	rejectedTurnRetentionType = "retentioner"
)

// rejectedTurnHistory returns the history of a workflow whose committed
// history holds task 1, named committed.
func rejectedTurnHistory(instanceID, committed string) []*backend.HistoryEvent {
	return []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
		},
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name:             "DeferSaga",
					Input:            wrapperspb.String(`0`),
					WorkflowInstance: &protos.WorkflowInstance{InstanceId: instanceID},
				},
			},
		},
		{
			EventId: 1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{Name: committed},
			},
		},
	}
}

// newRejectedTurnOrchestrator returns an orchestrator whose engine answers
// every turn with a response that re-creates task 1 under the name recreated,
// while the committed history holds task 1 as "Undo". The store it reads
// is the one in the returned pointer, so a test can swap the durable state.
func newRejectedTurnOrchestrator(t *testing.T, instanceID string) (*orchestrator, *atomic.Pointer[statefake.Fake], *atomic.Int32) {
	t.Helper()

	inbox := []*backend.HistoryEvent{{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "wake"}},
	}}

	// The state interface is resolved once, so it delegates to the durable
	// state a test can swap.
	var store atomic.Pointer[statefake.Fake]
	store.Store(fakeStoreServingState(t, 1, rejectedTurnHistory(instanceID, "Undo"), inbox))
	delegate := statefake.New().
		WithGetFn(func(ctx context.Context, req *actorapi.GetStateRequest, b bool) (*actorapi.StateResponse, error) {
			return store.Load().Get(ctx, req, b)
		}).
		WithGetBulkFn(func(ctx context.Context, req *actorapi.GetBulkStateRequest, b bool) (actorapi.BulkStateResponse, error) {
			return store.Load().GetBulk(ctx, req, b)
		})

	var turns atomic.Int32
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		turns.Add(1)
		wi.State.NewEvents = []*protos.HistoryEvent{{
			EventId: 1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{Name: "Step"},
			},
		}}
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
		return nil
	}

	fact, err := New(t.Context(), Options{
		AppID:              rejectedTurnTestApp,
		WorkflowActorType:  rejectedTurnWorkflowType,
		ActivityActorType:  rejectedTurnActivityType,
		RetentionActorType: rejectedTurnRetentionType,
		Scheduler:          scheduler,
		ActorTypeBuilder:   common.NewActorTypeBuilder("default"),
		Actors: fake.New().
			WithReminders(func(context.Context) (actorreminders.Interface, error) {
				return remindersfake.New(), nil
			}).
			WithState(func(context.Context) (actorstate.Interface, error) {
				return delegate, nil
			}),
	})
	require.NoError(t, err)

	return fact.GetOrCreate(instanceID).(*orchestrator), &store, &turns
}

func Test_invokeReminder_rejectedTurnHoldGrowsToACap(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-rejected-turn"
	o, _, turns := newRejectedTurnOrchestrator(t, instanceID)
	reminder := &actorapi.Reminder{Name: "new-event-tc-0"}

	var prev time.Duration
	for n := 1; n <= 15; n++ {
		hold, err := o.invokeReminder(t.Context(), reminder)
		require.Error(t, err)
		assert.True(t, wferrors.IsRecoverable(err), "a rejected turn is retried")
		assert.Equal(t, int32(n), turns.Load())

		assert.Equal(t, common.RejectedTurnBackoff(n), hold, "rejection %d", n)
		assert.GreaterOrEqual(t, hold, prev, "the hold never shrinks")
		assert.LessOrEqual(t, hold, common.RejectedTurnBackoffCap)
		prev = hold
	}
	assert.Equal(t, common.RejectedTurnBackoffCap, prev, "the hold reaches the cap and stays there")
	assert.Equal(t, 15, o.rejectedTurns)
	assert.Greater(t, prev, 100*common.RetryBackoffBase, "the hold grows well past the first retry interval")
}

func Test_invokeReminder_rejectedTurnErrorNamesTheClash(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-rejected-turn-error"
	o, _, _ := newRejectedTurnOrchestrator(t, instanceID)

	_, err := o.invokeReminder(t.Context(), &actorapi.Reminder{Name: "new-event-tc-0"})
	require.Error(t, err)
	require.ErrorContains(t, err, "task operation 1")
	require.ErrorContains(t, err, "committed 'Undo'")
	require.ErrorContains(t, err, "turn response 'Step'")
	require.ErrorContains(t, err, "not be deterministic")
}

func Test_invokeReminder_rejectedTurnCountResetsAfterASuccessfulTurn(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-rejected-turn-reset"
	o, store, _ := newRejectedTurnOrchestrator(t, instanceID)
	reminder := &actorapi.Reminder{Name: "new-event-tc-0"}

	for range 5 {
		_, err := o.invokeReminder(t.Context(), reminder)
		require.Error(t, err)
	}
	require.Equal(t, 5, o.rejectedTurns)

	// The instance is completed by then: the next wake-up acks.
	store.Store(fakeStoreServingState(t, 1, terminalHistory(instanceID), nil))
	hold, err := o.invokeReminder(t.Context(), reminder)
	require.NoError(t, err)
	assert.Zero(t, hold)
	assert.Zero(t, o.rejectedTurns, "a turn that succeeds resets the count")
}

func Test_invokeReminder_otherRecoverableErrorsAreNotHeld(t *testing.T) {
	t.Parallel()

	const instanceID = "wf-recoverable-turn"
	o, _, turns := newRejectedTurnOrchestrator(t, instanceID)

	// The engine abandons the work item: a recoverable error that is not a
	// rejected turn.
	o.scheduler = func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		turns.Add(1)
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- false
		return nil
	}

	for n := 1; n <= 5; n++ {
		hold, err := o.invokeReminder(t.Context(), &actorapi.Reminder{Name: "new-event-tc-0"})
		require.Error(t, err)
		assert.True(t, wferrors.IsRecoverable(err))
		assert.Zero(t, hold, "attempt %d: only a rejected turn is held back", n)
	}
	assert.Equal(t, int32(5), turns.Load())
	assert.Zero(t, o.rejectedTurns)
}

func Test_holdRejectedTurn(t *testing.T) {
	t.Parallel()

	start := time.Now()
	holdRejectedTurn(t.Context(), 0)
	holdRejectedTurn(t.Context(), 30*time.Millisecond)
	assert.GreaterOrEqual(t, time.Since(start), 30*time.Millisecond)

	// A canceled context ends the hold at once.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start = time.Now()
	holdRejectedTurn(ctx, time.Minute)
	assert.Less(t, time.Since(start), 10*time.Second)
}
