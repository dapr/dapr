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

package state

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/ptr"
)

var sevBase = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// sevAt returns the time i seconds after sevBase.
func sevAt(i int) time.Time {
	return sevBase.Add(time.Duration(i) * time.Second)
}

func sevTS(i int) *timestamppb.Timestamp {
	return timestamppb.New(sevAt(i))
}

func sevStarted(i int, version string) *backend.HistoryEvent {
	ws := &protos.WorkflowStartedEvent{}
	if version != "" {
		ws.Version = &protos.WorkflowVersion{Name: ptr.Of(version)}
	}
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: ws}}
}

func sevExecStarted(i int, name string, parent *protos.ParentInstanceInfo) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_ExecutionStarted{
		ExecutionStarted: &protos.ExecutionStartedEvent{Name: name, ParentInstance: parent},
	}}
}

func sevCompleted(i int, st protos.OrchestrationStatus) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_ExecutionCompleted{
		ExecutionCompleted: &protos.ExecutionCompletedEvent{WorkflowStatus: st},
	}}
}

func sevSuspended(i int) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}}}
}

func sevResumed(i int) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_ExecutionResumed{ExecutionResumed: &protos.ExecutionResumedEvent{}}}
}

func sevStalled(i int) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_ExecutionStalled{ExecutionStalled: &protos.ExecutionStalledEvent{}}}
}

func sevTask(i int, id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: id, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "act"}}}
}

func sevTaskDone(i int, id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: id}}}
}

func sevRaised(i int) *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: sevTS(i), EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "e"}}}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	const (
		running   = protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING
		completed = protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED
		failed    = protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED
		canceled  = protos.OrchestrationStatus_ORCHESTRATION_STATUS_CANCELED
		term      = protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED
		pending   = protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING
		suspended = protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED
		stalled   = protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED
	)

	parent := &protos.ParentInstanceInfo{
		WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent-1"},
		AppID:            ptr.Of("parent-app"),
	}
	running3 := func() []*backend.HistoryEvent {
		return []*backend.HistoryEvent{sevStarted(1, ""), sevExecStarted(0, "wf", nil), sevTask(2, 0)}
	}

	tests := map[string]struct {
		history []*backend.HistoryEvent
		inbox   []*backend.HistoryEvent
		want    *backend.BackendWorkflowSummary
	}{
		"empty": {
			want: &backend.BackendWorkflowSummary{RuntimeStatus: pending},
		},
		"pending from the inbox": {
			inbox: []*backend.HistoryEvent{sevRaised(9), sevExecStarted(5, "wf", parent)},
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: pending, CreatedAt: sevTS(5), LastUpdatedAt: sevTS(5),
				ParentInstanceId: "parent-1", ParentAppId: ptr.Of("parent-app"),
			},
		},
		"running": {
			history: running3(),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: running, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(2),
			},
		},
		"the history start wins over the inbox": {
			history: running3(),
			inbox:   []*backend.HistoryEvent{sevExecStarted(7, "other", parent)},
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: running, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(2),
			},
		},
		"suspended": {
			history: append(running3(), sevSuspended(3), sevRaised(4)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: suspended, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(4), Suspended: true,
			},
		},
		"suspend then resume": {
			history: append(running3(), sevSuspended(3), sevResumed(4)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: running, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(4),
			},
		},
		"suspend then complete": {
			history: append(running3(), sevSuspended(3), sevCompleted(4, term)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: term, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(4),
				CompletedAt: sevTS(4), Suspended: true,
			},
		},
		"stalled last": {
			history: append(running3(), sevStalled(3)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: stalled, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3),
			},
		},
		"stalled then progress": {
			history: append(running3(), sevStalled(3), sevTaskDone(4, 0)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: running, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(4),
			},
		},
		"stalled before start": {
			history: []*backend.HistoryEvent{sevStarted(1, ""), sevStalled(2)},
			inbox:   []*backend.HistoryEvent{sevExecStarted(0, "wf", nil)},
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: stalled, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(2),
			},
		},
		"completed": {
			history: append(running3(), sevCompleted(3, completed)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: completed, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3), CompletedAt: sevTS(3),
			},
		},
		"failed": {
			history: append(running3(), sevCompleted(3, failed)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: failed, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3), CompletedAt: sevTS(3),
			},
		},
		"canceled": {
			history: append(running3(), sevCompleted(3, canceled)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: canceled, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3), CompletedAt: sevTS(3),
			},
		},
		"terminated": {
			history: append(running3(), sevCompleted(3, term)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: term, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3), CompletedAt: sevTS(3),
			},
		},
		"tamper marker": {
			history: append(running3(), &backend.HistoryEvent{
				EventId: -1, Timestamp: sevTS(3), EventType: tamperMarkerEvent().GetEventType(),
			}),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: failed, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3), CompletedAt: sevTS(3),
			},
		},
		"continue-as-new: pending with the new start": {
			inbox: []*backend.HistoryEvent{sevExecStarted(50, "wf", nil), sevRaised(51)},
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: pending, CreatedAt: sevTS(50), LastUpdatedAt: sevTS(50),
			},
		},
		"version from the first WorkflowStarted": {
			history: []*backend.HistoryEvent{sevStarted(1, "v2"), sevExecStarted(0, "wf", nil), sevStarted(3, "v3")},
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: running, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3), Version: ptr.Of("v2"),
			},
		},
		"no version when the first WorkflowStarted has none": {
			history: []*backend.HistoryEvent{sevStarted(1, ""), sevExecStarted(0, "wf", nil), sevStarted(3, "v3")},
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: running, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3),
			},
		},
		"parent": {
			history: []*backend.HistoryEvent{sevStarted(1, ""), sevExecStarted(0, "child-wf", parent)},
			want: &backend.BackendWorkflowSummary{
				Name: "child-wf", RuntimeStatus: running, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(0),
				ParentInstanceId: "parent-1", ParentAppId: ptr.Of("parent-app"),
			},
		},
		"duplicate start and completion are ignored": {
			history: append(running3(), sevStalled(3), sevExecStarted(4, "again", nil), sevCompleted(5, failed), sevCompleted(6, completed)),
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: failed, CreatedAt: sevTS(0), StartedAt: sevTS(1), LastUpdatedAt: sevTS(5), CompletedAt: sevTS(5),
			},
		},
		"completion before start": {
			history: []*backend.HistoryEvent{sevStarted(1, ""), sevCompleted(2, failed), sevExecStarted(3, "wf", nil)},
			want: &backend.BackendWorkflowSummary{
				Name: "wf", RuntimeStatus: failed, CreatedAt: sevTS(3), StartedAt: sevTS(1), LastUpdatedAt: sevTS(3), CompletedAt: sevTS(2),
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := Summarize(test.history, test.inbox)
			assert.Truef(t, proto.Equal(test.want, got), "want %v\ngot  %v", test.want, got)
			if len(test.history) > 0 {
				rs := runtimestate.NewWorkflowRuntimeState("id", nil, test.history)
				assert.Equal(t, runtimestate.RuntimeStatus(rs), got.GetRuntimeStatus())
			}
		})
	}

	t.Run("long names are kept whole", func(t *testing.T) {
		t.Parallel()
		// Two names that have the same first 256 bytes.
		prefix := strings.Repeat("a", 256)
		first, second := prefix+"-first", prefix+"-second"
		a := Summarize([]*backend.HistoryEvent{sevExecStarted(0, first, nil)}, nil)
		b := Summarize([]*backend.HistoryEvent{sevExecStarted(0, second, nil)}, nil)
		assert.Equal(t, first, a.GetName())
		assert.Equal(t, second, b.GetName())
	})
}

func TestMatchesFilter(t *testing.T) {
	t.Parallel()

	done := &backend.BackendWorkflowSummary{
		Name: "wf", RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
		CreatedAt: sevTS(10), CompletedAt: sevTS(20),
	}
	running := &backend.BackendWorkflowSummary{
		Name: "other", RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, CreatedAt: sevTS(10),
	}
	// The call runs 30 seconds after the creation of both instances. The
	// completed instance ran for 10 seconds.
	now := sevTS(40).AsTime()
	sec := func(n int) *durationpb.Duration { return durationpb.New(time.Duration(n) * time.Second) }

	tests := map[string]struct {
		filter      *protos.ListInstancesFilter
		done, other bool
	}{
		"nil filter":            {filter: nil, done: true, other: true},
		"empty filter":          {filter: &protos.ListInstancesFilter{}, done: true, other: true},
		"status":                {filter: &protos.ListInstancesFilter{RuntimeStatus: []protos.OrchestrationStatus{protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING}}, other: true},
		"created from is in":    {filter: &protos.ListInstancesFilter{CreatedTimeFrom: sevTS(10)}, done: true, other: true},
		"created from after":    {filter: &protos.ListInstancesFilter{CreatedTimeFrom: sevTS(11)}},
		"created to is out":     {filter: &protos.ListInstancesFilter{CreatedTimeTo: sevTS(10)}},
		"created to after":      {filter: &protos.ListInstancesFilter{CreatedTimeTo: sevTS(11)}, done: true, other: true},
		"completed from is in":  {filter: &protos.ListInstancesFilter{CompletedTimeFrom: sevTS(20)}, done: true},
		"completed to is out":   {filter: &protos.ListInstancesFilter{CompletedTimeTo: sevTS(20)}},
		"completed to after":    {filter: &protos.ListInstancesFilter{CompletedTimeTo: sevTS(21)}, done: true},
		"all fields must match": {filter: &protos.ListInstancesFilter{RuntimeStatus: []protos.OrchestrationStatus{protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED}, CreatedTimeFrom: sevTS(11)}},

		"min duration is inclusive":           {filter: &protos.ListInstancesFilter{MinDuration: sec(10)}, done: true, other: true},
		"min duration above the duration":     {filter: &protos.ListInstancesFilter{MinDuration: sec(11)}, other: true},
		"max duration is exclusive":           {filter: &protos.ListInstancesFilter{MaxDuration: sec(10)}},
		"max duration above the duration":     {filter: &protos.ListInstancesFilter{MaxDuration: sec(11)}, done: true},
		"not completed uses now, min":         {filter: &protos.ListInstancesFilter{MinDuration: sec(30)}, other: true},
		"not completed uses now, min above":   {filter: &protos.ListInstancesFilter{MinDuration: sec(31)}},
		"not completed uses now, max":         {filter: &protos.ListInstancesFilter{MaxDuration: sec(30)}, done: true},
		"not completed uses now, max above":   {filter: &protos.ListInstancesFilter{MaxDuration: sec(31)}, done: true, other: true},
		"min and max":                         {filter: &protos.ListInstancesFilter{MinDuration: sec(10), MaxDuration: sec(30)}, done: true},
		"zero min is a bound that all meet":   {filter: &protos.ListInstancesFilter{MinDuration: sec(0)}, done: true, other: true},
		"unset bounds with other fields only": {filter: &protos.ListInstancesFilter{CreatedTimeTo: sevTS(11)}, done: true, other: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.done, MatchesFilter(done, test.filter, now))
			assert.Equal(t, test.other, MatchesFilter(running, test.filter, now))
		})
	}

	t.Run("an instance without a created time does not match a duration bound", func(t *testing.T) {
		t.Parallel()
		noCreated := &backend.BackendWorkflowSummary{RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING}
		assert.True(t, MatchesFilter(noCreated, &protos.ListInstancesFilter{}, now))
		assert.False(t, MatchesFilter(noCreated, &protos.ListInstancesFilter{MinDuration: sec(0)}, now))
		assert.False(t, MatchesFilter(noCreated, &protos.ListInstancesFilter{MaxDuration: sec(1000)}, now))
	})
}

// genHistory returns a random history as the engine can write it: event IDs
// and resolutions are unique, and every WorkflowStarted event has the same
// version. It also returns an inbox, which can hold the start event.
func genHistory(r *rand.Rand) (history, inbox []*backend.HistoryEvent) {
	n := r.IntN(25)
	version := ""
	if r.IntN(2) == 0 {
		version = "v1"
	}
	statuses := []protos.OrchestrationStatus{
		protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
		protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
		protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED,
		protos.OrchestrationStatus_ORCHESTRATION_STATUS_CANCELED,
	}

	var nextID int32
	var open []int32
	ts := 0
	for range n {
		ts += r.IntN(3)
		var e *backend.HistoryEvent
		switch k := r.IntN(12); k {
		case 0, 1:
			e = sevStarted(ts, version)
		case 2:
			e = sevExecStarted(ts, []string{"wfa", "wfb", "wfc"}[r.IntN(3)], nil)
		case 3:
			e = sevCompleted(ts, statuses[r.IntN(len(statuses))])
		case 4:
			e = sevSuspended(ts)
		case 5:
			e = sevResumed(ts)
		case 6, 7:
			e = sevStalled(ts)
		case 8:
			e = sevTask(ts, nextID)
			open = append(open, nextID)
			nextID++
		case 9:
			if len(open) == 0 {
				e = sevRaised(ts)
				break
			}
			i := r.IntN(len(open))
			e = sevTaskDone(ts, open[i])
			open = append(open[:i], open[i+1:]...)
		default:
			e = sevRaised(ts)
		}
		if r.IntN(40) == 0 {
			// runtimestate reads an unset timestamp as the Unix epoch.
			e.Timestamp = nil
		}
		history = append(history, e)
	}

	if r.IntN(2) == 0 {
		inbox = append(inbox, sevExecStarted(100, "inbox-wf", nil))
	}
	if r.IntN(2) == 0 {
		inbox = append(inbox, sevRaised(101))
	}
	return history, inbox
}

func TestSummaryProperties(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec
	for iter := range 5000 {
		history, inbox := genHistory(r)
		whole, wholeFacts := summarize(history, inbox)

		// advance over any split equals Summarize of the whole, with the
		// same facts. A load derives the summary and the facts of
		// history[:k], and the next save advances them.
		for k := 0; k <= len(history); k++ {
			base, facts := summarize(history[:k], inbox)
			got, gotFacts := advance(base, facts, history[k:], inbox)
			require.Truef(t, proto.Equal(whole, got), "iter %d split %d:\nwant %v\ngot  %v", iter, k, whole, got)
			require.Equalf(t, wholeFacts, gotFacts, "iter %d split %d", iter, k)
		}

		// A chain of random splits, as a run of saves makes.
		var sum *backend.BackendWorkflowSummary
		var facts summaryFacts
		for i := 0; i < len(history); {
			j := i + 1 + r.IntN(len(history)-i)
			sum, facts = advance(sum, facts, history[i:j], inbox)
			i = j
		}
		if len(history) > 0 {
			require.Truef(t, proto.Equal(whole, sum), "iter %d chain:\nwant %v\ngot  %v", iter, whole, sum)
		}

		// Summarize gives the values of runtimestate, which
		// GetWorkflowMetadata reports.
		rs := runtimestate.NewWorkflowRuntimeState("id", nil, history)
		got := Summarize(history, nil)
		require.Equalf(t, runtimestate.RuntimeStatus(rs), got.GetRuntimeStatus(), "iter %d: %v", iter, history)
		if rs.GetStartEvent() != nil {
			name, _ := runtimestate.Name(rs)
			created, _ := runtimestate.CreatedTime(rs)
			updated, _ := runtimestate.LastUpdatedTime(rs)
			assert.Equal(t, name, got.GetName())
			assert.Equal(t, created, got.GetCreatedAt().AsTime())
			assert.Equal(t, updated, got.GetLastUpdatedAt().AsTime())
			assert.Equal(t, runtimestate.GetStartedTime(rs), got.GetStartedAt().AsTime())
			assert.Equal(t, WorkflowVersion(rs.GetOldEvents()).GetValue(), got.GetVersion())
		}
		if completedAt, err := runtimestate.CompletedTime(rs); err == nil {
			assert.Equal(t, completedAt, got.GetCompletedAt().AsTime())
		} else {
			assert.Nil(t, got.GetCompletedAt())
		}
	}
}
