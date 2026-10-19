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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// handleReminder dispatches on name prefixes, so a held operation's name must
// not start with any name or prefix an earlier arm matches.
func TestHeldReminderNamesDoNotCollide(t *testing.T) {
	earlier := []string{
		janitorReminderName,
		reminderPrefixStart,
		reminderPrefixNewEvent,
		reminderPrefixTimer,
		reminderCascadeTerminate,
		reminderNameParentNotify,
		common.ReminderPrefixActivityResult,
	}

	for _, held := range []string{common.ReminderNameHeldStart, common.ReminderPrefixHeldEvent + "x"} {
		for _, prefix := range earlier {
			assert.Falsef(t, strings.HasPrefix(held, prefix), "held reminder name %q is caught by %q", held, prefix)
		}
	}
}

func heldReminder(t *testing.T, name string, m proto.Message) *actorapi.Reminder {
	t.Helper()
	data, err := anypb.New(m)
	require.NoError(t, err)
	return &actorapi.Reminder{Name: name, Data: data}
}

func heldEvent(at time.Time, e *protos.HistoryEvent) *protos.HistoryEvent {
	e.Timestamp = timestamppb.New(at)
	return e
}

// The scheduler fires held reminders in no particular order, so the replay
// orders them itself: the start first, then the events in the order they
// were requested, whatever their names.
func TestListHeldOrder(t *testing.T) {
	now := time.Now()
	suspend := heldEvent(now.Add(time.Second), &protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{}}})
	resume := heldEvent(now.Add(2*time.Second), &protos.HistoryEvent{EventType: &protos.HistoryEvent_ExecutionResumed{ExecutionResumed: &protos.ExecutionResumedEvent{}}})
	start := &backend.CreateWorkflowInstanceRequest{StartEvent: heldEvent(now.Add(3*time.Second), &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{Name: "wf"}},
	})}

	var deleted []string
	rem := remindersfake.New().
		WithList(func(_ context.Context, req *actorapi.ListRemindersRequest) ([]*actorapi.Reminder, error) {
			assert.Equal(t, "id", *req.ActorID)
			return []*actorapi.Reminder{
				heldReminder(t, common.ReminderPrefixHeldEvent+"a", resume),
				{Name: "timer-0"},
				heldReminder(t, common.ReminderPrefixHeldEvent+"b", suspend),
				nil,
				heldReminder(t, common.ReminderNameHeldStart, start),
				heldReminder(t, common.ReminderPrefixHeldEvent+"c", &protos.HistoryEvent{}),
				heldReminder(t, common.ReminderNameHeldStart+"x", &backend.CreateWorkflowInstanceRequest{}),
			}, nil
		}).
		WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deleted = append(deleted, req.Name)
			return nil
		})

	o := &orchestrator{
		factory: &factory{appID: "app", reminders: rem, actorTypeBuilder: common.NewActorTypeBuilder("ns")},
		actorID: "id",
	}

	ops, err := o.listHeld(t.Context())
	require.NoError(t, err)

	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.name
	}
	// held-event-c has no timestamp, so it sorts as requested first among
	// the events; held-startx is not a held start name and is ignored.
	assert.Equal(t, []string{
		common.ReminderNameHeldStart,
		common.ReminderPrefixHeldEvent + "c",
		common.ReminderPrefixHeldEvent + "b",
		common.ReminderPrefixHeldEvent + "a",
	}, names)
	assert.Empty(t, deleted)
}

// A held start without a start event cannot be replayed: it is deleted
// instead of being recorded as a failed instance with a nil event.
func TestListHeldDropsStartWithoutStartEvent(t *testing.T) {
	var deleted []string
	rem := remindersfake.New().
		WithList(func(context.Context, *actorapi.ListRemindersRequest) ([]*actorapi.Reminder, error) {
			return []*actorapi.Reminder{heldReminder(t, common.ReminderNameHeldStart, &backend.CreateWorkflowInstanceRequest{})}, nil
		}).
		WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deleted = append(deleted, req.Name)
			return nil
		})

	o := &orchestrator{
		factory: &factory{appID: "app", reminders: rem, actorTypeBuilder: common.NewActorTypeBuilder("ns")},
		actorID: "id",
	}

	ops, err := o.listHeld(t.Context())
	require.NoError(t, err)
	assert.Empty(t, ops)
	assert.Equal(t, []string{common.ReminderNameHeldStart}, deleted)
}

// A held reminder that fires after an earlier replay applied and deleted it
// is acked without replaying anything.
func TestReplayHeldAlreadyApplied(t *testing.T) {
	rem := remindersfake.New().
		WithList(func(context.Context, *actorapi.ListRemindersRequest) ([]*actorapi.Reminder, error) {
			return nil, nil
		}).
		WithDelete(func(context.Context, *actorapi.DeleteReminderRequest) error {
			t.Error("nothing should be deleted")
			return nil
		})

	o := &orchestrator{
		factory: &factory{appID: "app", reminders: rem, actorTypeBuilder: common.NewActorTypeBuilder("ns")},
		actorID: "id",
	}

	require.NoError(t, o.replayHeld(t.Context(), &actorapi.Reminder{Name: common.ReminderPrefixHeldEvent + "a"}))
}
