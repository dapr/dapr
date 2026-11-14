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

package ordering

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	keyactor "github.com/dapr/dapr/pkg/actors/targets/workflow/ordering"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

var keyK = &protos.WorkflowOrdering{Key: "k"}

func queued(ordering *protos.WorkflowOrdering) *wfenginestate.State {
	s := wfenginestate.NewState(wfenginestate.Options{})
	start := &backend.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
		Name:             "wf",
		WorkflowInstance: &protos.WorkflowInstance{InstanceId: "i", ExecutionId: wrapperspb.String("e1")},
		Ordering:         ordering,
	}}}
	s.StartOrdering(start.GetExecutionStarted())
	s.AddToInbox(start)
	return s
}

// finished is a keyed instance that ran, or not, and completed with status.
func finished(released bool, status protos.OrchestrationStatus) *wfenginestate.State {
	s := queued(keyK)
	if released {
		s.Ordering.Released = ptr.Of(true)
	}
	start := s.Inbox[0]
	s.ClearInbox()
	s.AddToHistory(start)
	s.AddToHistory(&backend.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{WorkflowStatus: status}}})
	return s
}

func reminder(t *testing.T, name, after string) *actorapi.Reminder {
	t.Helper()
	b, err := json.Marshal(keyactor.ReminderData{Key: "k", After: after})
	require.NoError(t, err)
	return &actorapi.Reminder{Name: name, Data: &anypb.Any{Value: b}}
}

var terminate = &backend.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionTerminated{ExecutionTerminated: &protos.ExecutionTerminatedEvent{}}}

func TestGate(t *testing.T) {
	run, term := Gate(&actorapi.Reminder{Name: "start-es-1"}, queued(nil))
	assert.True(t, run, "an unkeyed instance always runs")
	assert.Nil(t, term)
	run, term = Gate(&actorapi.Reminder{Name: "new-event-1"}, queued(keyK))
	assert.False(t, run, "a queued instance waits")
	assert.Nil(t, term)
	s := queued(keyK)
	run, _ = Gate(reminder(t, keyactor.ReleaseReminderName("e1"), ""), s)
	assert.True(t, run)
	assert.True(t, s.Ordering.GetReleased())
	run, _ = Gate(&actorapi.Reminder{Name: "new-event-1"}, s)
	assert.True(t, run, "a released instance runs every turn")

	run, term = Gate(reminder(t, keyactor.CancelReminderName("e1"), "s1"), queued(keyK))
	assert.False(t, run)
	assert.Contains(t, term.GetExecutionTerminated().GetInput().GetValue(), `workflow \"s1\" before it`)

	s = queued(keyK)
	s.AddToInbox(terminate)
	run, term = Gate(reminder(t, keyactor.ReleaseReminderName("e1"), ""), s)
	assert.False(t, run)
	assert.Same(t, terminate, term, "a terminate wins over a release")
	assert.False(t, s.Ordering.GetReleased(), "so the instance never ran")
}

func TestTerminateQueued(t *testing.T) {
	s := queued(keyK)
	var saved bool
	o := &Ordering{Save: func(context.Context, *wfenginestate.State) error { saved = true; return nil }}

	require.NoError(t, o.TerminateQueued(t.Context(), s, terminate))
	assert.True(t, saved)
	assert.Empty(t, s.Inbox)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED, s.History[len(s.History)-1].GetExecutionCompleted().GetWorkflowStatus())
}

func TestOutcome(t *testing.T) {
	assert.Equal(t, keyactor.OutcomeSkipped, outcome(finished(false, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED)))
	assert.Equal(t, keyactor.OutcomeSucceeded, outcome(finished(true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)))
	assert.Equal(t, keyactor.OutcomeFailed, outcome(finished(true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED)))
	assert.Equal(t, keyactor.OutcomeFailed, outcome(finished(true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED)))
}

// recorder is an Ordering whose key calls are recorded.
func recorder(t *testing.T) (*Ordering, *[]keyactor.DoneRequest) {
	var done []keyactor.DoneRequest
	o := &Ordering{
		ActorID:   "i",
		ActorType: "dapr.internal.ns.app.ordering",
		Router: routerfake.New().WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			assert.Equal(t, keyactor.DoneMethod, req.GetMessage().GetMethod())
			assert.Equal(t, "dapr.internal.ns.app.ordering", req.GetActor().GetActorType())
			var d keyactor.DoneRequest
			require.NoError(t, json.Unmarshal(req.GetMessage().GetData().GetValue(), &d))
			done = append(done, d)
			return &internalsv1pb.InternalInvokeResponse{}, nil
		}),
		Save:         func(context.Context, *wfenginestate.State) error { return nil },
		ArmDoneRetry: func(string) error { return nil },
	}
	return o, &done
}

func TestRelease(t *testing.T) {
	o, done := recorder(t)
	release := reminder(t, keyactor.ReleaseReminderName("e1"), "")

	live, err := o.Release(t.Context(), release, queued(keyK))
	require.NoError(t, err)
	assert.True(t, live, "the queued run takes its turn")
	assert.Empty(t, *done)

	live, err = o.Release(t.Context(), release, finished(true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED))
	require.NoError(t, err)
	assert.True(t, live, "a finished run settles in its turn")
	assert.Empty(t, *done)

	queuedOther := queued(keyK)
	queuedOther.Ordering.CreateExecutionId = "e2"
	for _, s := range []*wfenginestate.State{queuedOther, nil} {
		live, err = o.Release(t.Context(), reminder(t, keyactor.CancelReminderName("e1"), ""), s)
		require.NoError(t, err)
		assert.False(t, live)
	}
	outcomes := make([]keyactor.Outcome, 0, len(*done))
	for _, d := range *done {
		outcomes = append(outcomes, d.Outcome)
	}
	assert.Equal(t, []keyactor.Outcome{keyactor.OutcomeSkipped, keyactor.OutcomePurged}, outcomes, "another run is skipped, no state is purged")
}

func TestRecreate(t *testing.T) {
	o, done := recorder(t)
	next := func(key string) *protos.ExecutionStartedEvent {
		if key == "" {
			return &protos.ExecutionStartedEvent{}
		}
		return &protos.ExecutionStartedEvent{Ordering: &protos.WorkflowOrdering{Key: key}}
	}

	require.NoError(t, o.Recreate(t.Context(), finished(true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED), next("k")))
	require.Len(t, *done, 1, "same key: only the owed Done")
	assert.Equal(t, keyactor.OutcomeFailed, (*done)[0].Outcome)

	*done = nil
	require.NoError(t, o.Recreate(t.Context(), finished(true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED), next("")))
	require.Len(t, *done, 2, "leaving the key also skips the old run on it")
	assert.Equal(t, keyactor.OutcomePurged, (*done)[1].Outcome)
}
