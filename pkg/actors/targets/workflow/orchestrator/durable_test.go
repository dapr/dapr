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
	"errors"
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
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// durableTurn runs one workflow turn whose engine result is handed back with
// completed, against a store whose save calls save.
func durableTurn(t *testing.T, completed bool, save func(context.Context) error) (*orchestrator, <-chan *todo.Durable) {
	t.Helper()

	const instanceID = "durable-turn"

	start := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             "TestWorkflow",
				Input:            wrapperspb.String(`0`),
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: instanceID},
			},
		},
	}
	history := []*backend.HistoryEvent{{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
	}, start}
	inbox := []*backend.HistoryEvent{{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "go"}},
	}}

	durables := make(chan *todo.Durable, 1)
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		durable, ok := wi.Properties[todo.DurableProperty].(*todo.Durable)
		require.True(t, ok, "the work item must carry a Durable")
		durables <- durable
		durable.Consume()
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- completed
		return nil
	}

	fakeState := statefake.New().
		WithTransactionalStateOperationFn(func(ctx context.Context, _ bool, _ *actorapi.TransactionalRequest, _ bool) error {
			return save(ctx)
		})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
		Scheduler:         scheduler,
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors: fake.New().
			WithReminders(func(context.Context) (actorreminders.Interface, error) { return remindersfake.New(), nil }).
			WithState(func(context.Context) (actorstate.Interface, error) { return fakeState, nil }),
	})
	require.NoError(t, err)

	o := fact.GetOrCreate(instanceID).(*orchestrator)
	st := wfenginestate.NewState(wfenginestate.Options{AppID: "testapp", WorkflowActorType: "workflow", ActivityActorType: "activity"})
	for _, e := range history {
		st.AddToHistory(e)
	}
	for _, e := range inbox {
		st.AddToInbox(e)
	}
	o.state = st
	o.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
	o.ometa = o.ometaFromState(o.rstate, start.GetExecutionStarted())
	return o, durables
}

func requireSettled(t *testing.T, durable *todo.Durable) error {
	t.Helper()
	select {
	case <-durable.Done():
		return durable.Err()
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the turn's Durable was not settled")
		return nil
	}
}

func Test_runWorkflow_settlesDurableAfterSave(t *testing.T) {
	t.Parallel()

	t.Run("settled only once the save returns", func(t *testing.T) {
		t.Parallel()

		saving := make(chan struct{})
		release := make(chan struct{})
		o, durables := durableTurn(t, true, func(context.Context) error {
			close(saving)
			<-release
			return nil
		})

		runErr := make(chan error, 1)
		go func() {
			_, err := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-durable"})
			runErr <- err
		}()

		durable := <-durables
		<-saving
		select {
		case <-durable.Done():
			require.FailNow(t, "the Durable was settled before the save returned")
		case <-time.After(200 * time.Millisecond):
		}

		close(release)
		require.NoError(t, requireSettled(t, durable))
		require.NoError(t, <-runErr)
	})

	t.Run("a failed save settles with its error", func(t *testing.T) {
		t.Parallel()

		saveErr := errors.New("state store unavailable")
		o, durables := durableTurn(t, true, func(context.Context) error { return saveErr })

		_, err := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-durable"})
		require.Error(t, err)

		settled := requireSettled(t, <-durables)
		require.ErrorIs(t, settled, saveErr)
	})

	t.Run("an abandoned turn settles with an error", func(t *testing.T) {
		t.Parallel()

		o, durables := durableTurn(t, false, func(context.Context) error { return nil })

		_, err := o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-durable"})
		require.Error(t, err)
		assert.Error(t, requireSettled(t, <-durables))
	})
}
