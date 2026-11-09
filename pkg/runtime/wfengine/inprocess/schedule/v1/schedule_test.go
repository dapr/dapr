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

package schedule

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/ptr"
)

var t0 = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

const every1h = "@every 1h"

func TestNext(t *testing.T) {
	at := func(h, m int) time.Time { return t0.Add(time.Duration(h-10)*time.Hour + time.Duration(m)*time.Minute) }
	tests := map[string]struct {
		ws   *protos.WorkflowSchedule
		s    state
		now  time.Time
		want time.Time
		ok   bool
	}{
		"cron": {
			ws: &protos.WorkflowSchedule{Schedule: "0 0 9 * * *"}, now: at(10, 0),
			want: at(33, 0), ok: true,
		},
		"cron with a time zone": {
			ws: &protos.WorkflowSchedule{Schedule: "CRON_TZ=Asia/Tokyo 0 0 9 * * *"}, now: at(10, 0),
			want: at(24, 0), ok: true,
		},
		"every keeps its cadence": {
			ws: &protos.WorkflowSchedule{Schedule: every1h}, s: state{LastScheduled: ptr.Of(at(9, 0))}, now: at(9, 0).Add(time.Second),
			want: at(10, 0), ok: true,
		},
		"every catches up each time missed while down": {
			ws: &protos.WorkflowSchedule{Schedule: every1h}, s: state{LastScheduled: ptr.Of(at(6, 0))}, now: at(9, 30),
			want: at(7, 0), ok: true,
		},
		"cron catches up from two days behind": {
			ws: &protos.WorkflowSchedule{Schedule: "0 0 9 * * *"}, s: state{LastScheduled: ptr.Of(at(9-48, 0))}, now: at(10, 0),
			want: at(9-24, 0), ok: true,
		},
		"skipMissed skips times missed while down": {
			ws: &protos.WorkflowSchedule{Schedule: every1h, SkipMissed: ptr.Of(true)}, s: state{LastScheduled: ptr.Of(at(6, 0))}, now: at(9, 30),
			want: at(10, 30), ok: true,
		},
		"skipMissed set to false catches up": {
			ws: &protos.WorkflowSchedule{Schedule: every1h, SkipMissed: ptr.Of(false)}, s: state{LastScheduled: ptr.Of(at(6, 0))}, now: at(9, 30),
			want: at(7, 0), ok: true,
		},
		"early timer does not repeat a time": {
			ws: &protos.WorkflowSchedule{Schedule: "0 0 * * * *"}, s: state{LastScheduled: ptr.Of(at(10, 0))}, now: at(10, 0).Add(-time.Millisecond),
			want: at(11, 0), ok: true,
		},
		"due time first": {
			ws: &protos.WorkflowSchedule{Schedule: every1h, DueTime: timestamppb.New(at(12, 15))}, now: at(10, 0),
			want: at(12, 15), ok: true,
		},
		"repeats reached": {
			ws: &protos.WorkflowSchedule{Schedule: every1h, Repeats: ptr.Of(uint32(2))}, s: state{RunCount: 2}, now: at(10, 0),
		},
		"after expiration": {
			ws: &protos.WorkflowSchedule{Schedule: every1h, ExpirationTime: timestamppb.New(at(10, 30))}, now: at(10, 0),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			sched, err := parse(test.ws.GetSchedule())
			require.NoError(t, err)
			got, ok := next(test.ws, sched, &test.s, test.now)
			assert.Equal(t, test.ok, ok)
			if test.ok {
				assert.True(t, test.want.Equal(got), "want %s, got %s", test.want, got)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	valid := func() *protos.WorkflowSchedule {
		return &protos.WorkflowSchedule{Name: "nightly-1", WorkflowName: "wf", Schedule: "0 0 2 * * *"}
	}
	require.NoError(t, validate(valid(), t0))

	for name, mutate := range map[string]func(*protos.WorkflowSchedule){
		"name with underscore": func(ws *protos.WorkflowSchedule) { ws.Name = "a_b" },
		"name too long":        func(ws *protos.WorkflowSchedule) { ws.Name = "a123456789a123456789a123456789a" },
		"no workflow":          func(ws *protos.WorkflowSchedule) { ws.WorkflowName = "" },
		"schedule workflow":    func(ws *protos.WorkflowSchedule) { ws.WorkflowName = WorkflowName },
		"bad cron":             func(ws *protos.WorkflowSchedule) { ws.Schedule = "0 0 2 * *" },
		"sub-second every":     func(ws *protos.WorkflowSchedule) { ws.Schedule = "@every 100ms" },
		"never matches":        func(ws *protos.WorkflowSchedule) { ws.Schedule = "0 0 0 30 2 *" },
		"zero repeats":         func(ws *protos.WorkflowSchedule) { ws.Repeats = ptr.Of(uint32(0)) },
		"expired":              func(ws *protos.WorkflowSchedule) { ws.ExpirationTime = timestamppb.New(t0) },
		"namespace no app":     func(ws *protos.WorkflowSchedule) { ws.TargetAppNamespace = ptr.Of("ns") },
	} {
		t.Run(name, func(t *testing.T) {
			ws := valid()
			mutate(ws)
			assert.Error(t, validate(ws, t0))
		})
	}
}

func TestRunInstanceID(t *testing.T) {
	owner := Owner("default", "app")
	assert.Len(t, owner, 6)
	assert.Equal(t, owner, Owner("default", "app"))
	assert.NotEqual(t, owner, Owner("default", "app2"))
	assert.NotEqual(t, owner, Owner("other", "app"))

	at := time.Date(2026, 10, 29, 10, 0, 10, 123456789, time.UTC)
	assert.Equal(t, "s_"+owner+"_20261029T100010Z", RunInstanceID("s", owner, at))
	assert.Equal(t, "s_"+owner+"_20261029T100010123Z_manual", ManualRunInstanceID("s", owner, at))

	longest := ManualRunInstanceID("a123456789a123456789a123456789", owner, at)
	assert.Len(t, longest, 64, "a run ID of the longest name fits the instance ID limit")
}

func TestCheckCreate(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, CheckCreate(ctx, "wf", "abc"))
	assert.Equal(t, codes.InvalidArgument, status.Code(CheckCreate(ctx, WorkflowName, "abc")))
	assert.Equal(t, codes.InvalidArgument, status.Code(CheckCreate(ctx, "wf", InstanceIDPrefix+"x")))
	assert.NoError(t, CheckCreate(context.WithValue(ctx, createKey{}, true), WorkflowName, InstanceIDPrefix+"x"))
}

// TestRunTurns replays the schedule workflow turn by turn, as the runtime
// drives it.
func TestRunTurns(t *testing.T) {
	ws := &protos.WorkflowSchedule{Name: "s", WorkflowName: "wf", Schedule: "@every 10s", Input: wrapperspb.String(`"in"`)}

	t.Run("a new schedule waits for its first time", func(t *testing.T) {
		resp := execute(t, nil, begin(t0, input(t, ws, state{})))
		require.Len(t, resp.GetActions(), 1)
		timer := resp.GetActions()[0].GetCreateTimer()
		require.NotNil(t, timer)
		assert.Equal(t, t0.Add(10*time.Second), timer.GetFireAt().AsTime())
		st := customStatus(t, resp)
		assert.Equal(t, protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_ACTIVE, st.GetState())
		assert.Equal(t, t0.Add(10*time.Second), st.GetNextRunTime().AsTime())
	})

	t.Run("the timer firing only continues as new", func(t *testing.T) {
		first := execute(t, nil, begin(t0, input(t, ws, state{})))
		timer := first.GetActions()[0]
		old := append(begin(t0, input(t, ws, state{})), timerCreated(timer))
		resp := execute(t, old, []*protos.HistoryEvent{
			started(t0.Add(10 * time.Second)),
			{EventId: -1, Timestamp: timestamppb.New(t0.Add(10 * time.Second)), EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{TimerId: timer.GetId(), FireAt: timer.GetCreateTimer().GetFireAt()},
			}},
		})
		require.Len(t, resp.GetActions(), 1)
		done := resp.GetActions()[0].GetCompleteWorkflow()
		require.NotNil(t, done)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_CONTINUED_AS_NEW, done.GetWorkflowStatus())
		var s state
		require.NoError(t, json.Unmarshal([]byte(done.GetResult().GetValue()), &s))
		require.NotNil(t, s.Due)
		assert.Equal(t, t0.Add(10*time.Second), *s.Due)
	})

	due := t0.Add(10 * time.Second)

	t.Run("the next generation starts the due run, then waits", func(t *testing.T) {
		resp := execute(t, nil, begin(due, input(t, ws, state{Due: &due, Owner: "abc123"})))
		require.Len(t, resp.GetActions(), 2)
		run := resp.GetActions()[0].GetCreateDetachedWorkflow()
		require.NotNil(t, run)
		assert.Equal(t, "s_abc123_20261029T100010Z", run.GetInstanceId())
		assert.Equal(t, "wf", run.GetName())
		assert.Equal(t, `"in"`, run.GetInput().GetValue())
		assert.Equal(t, due.Add(10*time.Second), resp.GetActions()[1].GetCreateTimer().GetFireAt().AsTime())
		st := customStatus(t, resp)
		assert.Equal(t, uint32(1), st.GetRunCount())
		assert.Equal(t, "s_abc123_20261029T100010Z", st.GetLastRun().GetInstanceId())
	})

	t.Run("a trigger in the same turn starts its run without continuing as new", func(t *testing.T) {
		events := append(begin(due, input(t, ws, state{Due: &due, Owner: "abc123"})), &protos.HistoryEvent{
			EventId: -1, Timestamp: timestamppb.New(due),
			EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{
				Name: triggerEvent, Input: wrapperspb.String(`{"instanceId":"s_manual"}`),
			}},
		})
		resp := execute(t, nil, events)
		// The wait the trigger ends inline still emits its timer, which fires
		// with the next one and is ignored.
		var runs []string
		for _, a := range resp.GetActions() {
			if d := a.GetCreateDetachedWorkflow(); d != nil {
				runs = append(runs, d.GetInstanceId())
			}
			assert.Nil(t, a.GetCompleteWorkflow(), "a run and ContinueAsNew must not share a turn")
		}
		assert.Equal(t, []string{"s_abc123_20261029T100010Z", "s_manual"}, runs)
		last := resp.GetActions()[len(resp.GetActions())-1].GetCreateTimer()
		require.NotNil(t, last)
		assert.Equal(t, due.Add(10*time.Second), last.GetFireAt().AsTime())
		assert.Equal(t, uint32(2), customStatus(t, resp).GetRunCount())
	})

	t.Run("a generation behind starts one missed run and waits for no time", func(t *testing.T) {
		now := due.Add(time.Hour)
		resp := execute(t, nil, begin(now, input(t, ws, state{Due: &due, Owner: "abc123"})))
		require.Len(t, resp.GetActions(), 2)
		assert.Equal(t, "s_abc123_20261029T100010Z", resp.GetActions()[0].GetCreateDetachedWorkflow().GetInstanceId())
		timer := resp.GetActions()[1].GetCreateTimer()
		require.NotNil(t, timer)
		assert.WithinDuration(t, now, timer.GetFireAt().AsTime(), time.Millisecond)
		assert.Equal(t, due.Add(10*time.Second), customStatus(t, resp).GetNextRunTime().AsTime(),
			"the next run is the next missed time, not the next time after now")
	})

	t.Run("the last repeat completes the schedule", func(t *testing.T) {
		last := &protos.WorkflowSchedule{Name: "s", WorkflowName: "wf", Schedule: "@every 10s", Repeats: ptr.Of(uint32(1))}
		resp := execute(t, nil, begin(due, input(t, last, state{Due: &due, Owner: "abc123"})))
		require.Len(t, resp.GetActions(), 2)
		assert.NotNil(t, resp.GetActions()[0].GetCreateDetachedWorkflow())
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, resp.GetActions()[1].GetCompleteWorkflow().GetWorkflowStatus())
		assert.Equal(t, protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_COMPLETED, customStatus(t, resp).GetState())
	})
}

func execute(t *testing.T, old, nw []*protos.HistoryEvent) *protos.WorkflowResponse {
	t.Helper()
	r := task.NewTaskRegistry()
	Register(r)
	resp, err := task.NewTaskExecutor(r).ExecuteWorkflow(context.Background(), InstanceIDPrefix+"s", old, nw, backend.ExecuteOptions{})
	require.NoError(t, err)
	return resp
}

func input(t *testing.T, ws *protos.WorkflowSchedule, s state) string {
	t.Helper()
	spec, err := protojson.Marshal(ws)
	require.NoError(t, err)
	s.Schedule, s.CreatedAt = spec, t0
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

func started(at time.Time) *protos.HistoryEvent {
	return &protos.HistoryEvent{EventId: -1, Timestamp: timestamppb.New(at), EventType: &protos.HistoryEvent_WorkflowStarted{
		WorkflowStarted: &protos.WorkflowStartedEvent{},
	}}
}

func begin(at time.Time, in string) []*protos.HistoryEvent {
	return []*protos.HistoryEvent{
		started(at),
		{EventId: -1, Timestamp: timestamppb.New(at), EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             WorkflowName,
				Input:            wrapperspb.String(in),
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: InstanceIDPrefix + "s", ExecutionId: wrapperspb.String("e")},
			},
		}},
	}
}

func timerCreated(a *protos.WorkflowAction) *protos.HistoryEvent {
	ct := a.GetCreateTimer()
	return &protos.HistoryEvent{EventId: a.GetId(), Timestamp: timestamppb.New(t0), EventType: &protos.HistoryEvent_TimerCreated{
		TimerCreated: &protos.TimerCreatedEvent{
			FireAt: ct.GetFireAt(), Name: ct.Name,
			Origin: &protos.TimerCreatedEvent_ExternalEvent{ExternalEvent: ct.GetExternalEvent()},
		},
	}}
}

func customStatus(t *testing.T, resp *protos.WorkflowResponse) *protos.WorkflowScheduleStatus {
	t.Helper()
	var st protos.WorkflowScheduleStatus
	require.NoError(t, protojson.Unmarshal([]byte(resp.GetCustomStatus().GetValue()), &st))
	return &st
}
