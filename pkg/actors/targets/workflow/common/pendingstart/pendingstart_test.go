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

package pendingstart

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func pendingState(inbox ...*backend.HistoryEvent) *wfenginestate.State {
	s := wfenginestate.NewState(wfenginestate.Options{})
	for _, e := range inbox {
		s.AddToInbox(e)
	}
	return s
}

func startEvent(ts time.Time, scheduled *time.Time) *backend.HistoryEvent {
	es := &protos.ExecutionStartedEvent{Name: "wf"}
	if scheduled != nil {
		es.ScheduledStartTimestamp = timestamppb.New(*scheduled)
	}
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(ts),
		EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: es},
	}
}

func Test_Overdue(t *testing.T) {
	t.Parallel()

	now := time.Now()
	grace := RedriveGrace()
	raised := &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(now),
		EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "e"}},
	}

	t.Run("nil state", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Overdue(nil, now))
	})

	t.Run("no pending start", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Overdue(pendingState(raised), now))
	})

	t.Run("within grace", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Overdue(pendingState(startEvent(now.Add(-grace/2), nil)), now))
	})

	t.Run("past grace", func(t *testing.T) {
		t.Parallel()
		ev := startEvent(now.Add(-grace-time.Second), nil)
		assert.Same(t, ev, Overdue(pendingState(raised, ev), now),
			"the ExecutionStarted is found behind other inbox rows")
	})

	t.Run("delayed start measured from its scheduled time", func(t *testing.T) {
		t.Parallel()
		future := now.Add(time.Hour)
		assert.Nil(t, Overdue(pendingState(startEvent(now.Add(-time.Hour), &future)), now))
		past := now.Add(-grace - time.Second)
		assert.NotNil(t, Overdue(pendingState(startEvent(now, &past)), now))
	})

	t.Run("started instance", func(t *testing.T) {
		t.Parallel()
		s := pendingState(startEvent(now.Add(-time.Hour), nil))
		s.AddToHistory(&backend.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.New(now),
			EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
		})
		assert.Nil(t, Overdue(s, now))
	})
}
