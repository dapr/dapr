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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/durabletask-go/api/protos"
)

func Test_timerReminderName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		timerID int32
		want    string
	}{
		{0, "timer-0"},
		{1, "timer-1"},
		{42, "timer-42"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, timerReminderName(tt.timerID))
	}
}

func Test_hasUnfiredTimers(t *testing.T) {
	t.Parallel()

	timerCreated := func(eventID int32) *protos.HistoryEvent {
		return &protos.HistoryEvent{
			EventId: eventID,
			EventType: &protos.HistoryEvent_TimerCreated{
				TimerCreated: &protos.TimerCreatedEvent{},
			},
		}
	}

	timerFired := func(timerID int32) *protos.HistoryEvent {
		return &protos.HistoryEvent{
			EventId: -1,
			EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{
					TimerId: timerID,
				},
			},
		}
	}

	t.Run("empty state returns false", func(t *testing.T) {
		t.Parallel()
		assert.False(t, hasUnfiredTimers(&protos.WorkflowRuntimeState{}))
	})

	t.Run("no timers returns false", func(t *testing.T) {
		t.Parallel()
		rs := &protos.WorkflowRuntimeState{
			NewEvents: []*protos.HistoryEvent{
				{EventType: &protos.HistoryEvent_EventRaised{
					EventRaised: &protos.EventRaisedEvent{Name: "foo"},
				}},
			},
		}
		assert.False(t, hasUnfiredTimers(rs))
	})

	t.Run("created without fired returns true", func(t *testing.T) {
		t.Parallel()
		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{timerCreated(0)},
		}
		assert.True(t, hasUnfiredTimers(rs))
	})

	t.Run("created and fired returns false", func(t *testing.T) {
		t.Parallel()
		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{timerCreated(0), timerFired(0)},
		}
		assert.False(t, hasUnfiredTimers(rs))
	})

	t.Run("two created one fired returns true", func(t *testing.T) {
		t.Parallel()
		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{timerCreated(0), timerCreated(1)},
			NewEvents: []*protos.HistoryEvent{timerFired(0)},
		}
		assert.True(t, hasUnfiredTimers(rs))
	})

	t.Run("all fired returns false", func(t *testing.T) {
		t.Parallel()
		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{timerCreated(0), timerCreated(1), timerFired(0)},
			NewEvents: []*protos.HistoryEvent{timerFired(1)},
		}
		assert.False(t, hasUnfiredTimers(rs))
	})

	t.Run("timer in NewEvents only returns true", func(t *testing.T) {
		t.Parallel()
		rs := &protos.WorkflowRuntimeState{
			NewEvents: []*protos.HistoryEvent{timerCreated(0)},
		}
		assert.True(t, hasUnfiredTimers(rs))
	})
}

func Test_deleteAllReminders(t *testing.T) {
	t.Parallel()

	newOrchestrator := func(reminders *remindersfake.Fake) *orchestrator {
		return &orchestrator{
			factory: &factory{
				appID:             "testapp",
				activityActorType: "dapr.internal.default.testapp.activity",
				reminders:         reminders,
				actorTypeBuilder:  common.NewActorTypeBuilder("default"),
			},
			actorID: "wf-123",
		}
	}

	t.Run("deletes workflow and activity reminders", func(t *testing.T) {
		t.Parallel()
		var gotReqs []*actorapi.DeleteRemindersByActorIDRequest
		reminders := remindersfake.New().WithDeleteByActorID(func(_ context.Context, req *actorapi.DeleteRemindersByActorIDRequest) error {
			gotReqs = append(gotReqs, req)
			return nil
		})
		o := newOrchestrator(reminders)

		err := o.deleteAllReminders(t.Context())
		require.NoError(t, err)
		require.Len(t, gotReqs, 2)

		assert.Equal(t, "dapr.internal.default.testapp.workflow", gotReqs[0].ActorType)
		assert.Equal(t, "wf-123", gotReqs[0].ActorID)
		assert.False(t, gotReqs[0].MatchIDAsPrefix)

		assert.Equal(t, "dapr.internal.default.testapp.activity", gotReqs[1].ActorType)
		assert.Equal(t, "wf-123::", gotReqs[1].ActorID)
		assert.True(t, gotReqs[1].MatchIDAsPrefix)
	})

	t.Run("workflow reminder delete error stops and returns", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		reminders := remindersfake.New().WithDeleteByActorID(func(_ context.Context, _ *actorapi.DeleteRemindersByActorIDRequest) error {
			callCount++
			return errors.New("scheduler unavailable")
		})
		o := newOrchestrator(reminders)

		err := o.deleteAllReminders(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scheduler unavailable")
		assert.Equal(t, 1, callCount)
	})

	t.Run("activity reminder delete error is returned", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		reminders := remindersfake.New().WithDeleteByActorID(func(_ context.Context, _ *actorapi.DeleteRemindersByActorIDRequest) error {
			callCount++
			if callCount == 1 {
				return nil
			}
			return errors.New("activity delete failed")
		})
		o := newOrchestrator(reminders)

		err := o.deleteAllReminders(t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "activity delete failed")
		assert.Equal(t, 2, callCount)
	})
}

func Test_deleteCancelledEventTimers(t *testing.T) {
	t.Parallel()

	eventName := func(name string) *string { return &name }

	timerCreated := func(eventID int32, name *string) *protos.HistoryEvent {
		return &protos.HistoryEvent{
			EventId: eventID,
			EventType: &protos.HistoryEvent_TimerCreated{
				TimerCreated: &protos.TimerCreatedEvent{
					Name: name,
				},
			},
		}
	}

	timerCreatedWithOrigin := func(eventID int32, eventName string) *protos.HistoryEvent {
		return &protos.HistoryEvent{
			EventId: eventID,
			EventType: &protos.HistoryEvent_TimerCreated{
				TimerCreated: &protos.TimerCreatedEvent{
					Name: &eventName,
					Origin: &protos.TimerCreatedEvent_ExternalEvent{
						ExternalEvent: &protos.TimerOriginExternalEvent{
							Name: eventName,
						},
					},
				},
			},
		}
	}

	timerFired := func(timerID int32) *protos.HistoryEvent {
		return &protos.HistoryEvent{
			EventId: -1,
			EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{
					TimerId: timerID,
				},
			},
		}
	}

	eventRaised := func(name string) *protos.HistoryEvent {
		return &protos.HistoryEvent{
			EventId: -1,
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: &protos.EventRaisedEvent{
					Name: name,
				},
			},
		}
	}

	newOrchestrator := func(reminders *remindersfake.Fake) *orchestrator {
		return &orchestrator{
			factory: &factory{
				appID:            "testapp",
				reminders:        reminders,
				actorTypeBuilder: common.NewActorTypeBuilder("default"),
			},
			actorID: "wf-123",
		}
	}

	t.Run("no new events returns nil", func(t *testing.T) {
		t.Parallel()
		o := newOrchestrator(remindersfake.New())

		err := o.deleteCancelledEventTimers(t.Context(), &protos.WorkflowRuntimeState{})
		require.NoError(t, err)
	})

	t.Run("no EventRaised in new events returns nil", func(t *testing.T) {
		t.Parallel()
		o := newOrchestrator(remindersfake.New())

		rs := &protos.WorkflowRuntimeState{
			NewEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
	})

	t.Run("EventRaised with no matching timer does not delete", func(t *testing.T) {
		t.Parallel()
		deleteCalled := false
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			deleteCalled = true
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.False(t, deleteCalled)
	})

	t.Run("EventRaised matches timer in OldEvents and deletes it", func(t *testing.T) {
		t.Parallel()
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Equal(t, []string{"timer-0"}, deletedNames)
	})

	t.Run("EventRaised matches timer in NewEvents and deletes it", func(t *testing.T) {
		t.Parallel()
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			NewEvents: []*protos.HistoryEvent{
				timerCreated(5, eventName("bar")),
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Equal(t, []string{"timer-5"}, deletedNames)
	})

	t.Run("event name matching is case insensitive", func(t *testing.T) {
		t.Parallel()
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("MyEvent")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("MYEVENT"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Equal(t, []string{"timer-0"}, deletedNames)
	})

	t.Run("already fired timer is not cancelled", func(t *testing.T) {
		t.Parallel()
		deleteCalled := false
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			deleteCalled = true
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
				timerFired(0),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.False(t, deleteCalled)
	})

	t.Run("origin.external_event with Name matches and deletes timer", func(t *testing.T) {
		t.Parallel()
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.OrchestrationRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreatedWithOrigin(3, "myevent"),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("myevent"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Equal(t, []string{"timer-3"}, deletedNames)
	})

	t.Run("timer without Name field is ignored", func(t *testing.T) {
		t.Parallel()
		deleteCalled := false
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			deleteCalled = true
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, nil), // plain timer, not from WaitForSingleEvent
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.False(t, deleteCalled)
	})

	t.Run("FIFO: one EventRaised consumes only the first matching timer", func(t *testing.T) {
		t.Parallel()
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
				timerCreated(1, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Equal(t, []string{"timer-0"}, deletedNames)
	})

	t.Run("multiple EventRaised consume multiple timers FIFO", func(t *testing.T) {
		t.Parallel()
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
				timerCreated(1, eventName("bar")),
				timerCreated(2, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Equal(t, []string{"timer-0", "timer-1"}, deletedNames)
	})

	t.Run("multiple event names handled independently", func(t *testing.T) {
		t.Parallel()
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("foo")),
				timerCreated(1, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("foo"),
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Len(t, deletedNames, 2)
		assert.Contains(t, deletedNames, "timer-0")
		assert.Contains(t, deletedNames, "timer-1")
	})

	t.Run("delete request uses correct actor type and ID", func(t *testing.T) {
		t.Parallel()
		var gotReq *actorapi.DeleteReminderRequest
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			gotReq = req
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		require.NotNil(t, gotReq)
		assert.Equal(t, "timer-0", gotReq.Name)
		assert.Equal(t, "dapr.internal.default.testapp.workflow", gotReq.ActorType)
		assert.Equal(t, "wf-123", gotReq.ActorID)
	})

	t.Run("NotFound error from delete is ignored", func(t *testing.T) {
		t.Parallel()
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			return status.Error(codes.NotFound, "job not found")
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
	})

	t.Run("non-NotFound error from delete is returned", func(t *testing.T) {
		t.Parallel()
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			return errors.New("connection refused")
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("gRPC error other than NotFound is returned", func(t *testing.T) {
		t.Parallel()
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			return status.Error(codes.Internal, "internal error")
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "internal error")
	})

	t.Run("NotFound on first delete does not prevent second delete", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		var deletedNames []string
		reminders := remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			callCount++
			if callCount == 1 {
				return status.Error(codes.NotFound, "job not found")
			}
			deletedNames = append(deletedNames, req.Name)
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
				timerCreated(1, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("bar"),
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.Equal(t, 2, callCount)
		assert.Equal(t, []string{"timer-1"}, deletedNames)
	})

	t.Run("error on first delete stops processing", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			callCount++
			if callCount == 1 {
				return errors.New("connection refused")
			}
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("foo")),
				timerCreated(1, eventName("bar")),
			},
			NewEvents: []*protos.HistoryEvent{
				eventRaised("foo"),
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.Error(t, err)
		assert.Equal(t, 1, callCount)
	})

	t.Run("EventRaised in OldEvents does not trigger deletion", func(t *testing.T) {
		t.Parallel()
		deleteCalled := false
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			deleteCalled = true
			return nil
		})
		o := newOrchestrator(reminders)

		// Both the timer and event are in OldEvents (already processed).
		// No new EventRaised in NewEvents, so nothing should be deleted.
		rs := &protos.WorkflowRuntimeState{
			OldEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
				eventRaised("bar"),
			},
			NewEvents: []*protos.HistoryEvent{},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.False(t, deleteCalled)
	})

	t.Run("fired timer in NewEvents is excluded", func(t *testing.T) {
		t.Parallel()
		deleteCalled := false
		reminders := remindersfake.New().WithDelete(func(_ context.Context, _ *actorapi.DeleteReminderRequest) error {
			deleteCalled = true
			return nil
		})
		o := newOrchestrator(reminders)

		rs := &protos.WorkflowRuntimeState{
			NewEvents: []*protos.HistoryEvent{
				timerCreated(0, eventName("bar")),
				timerFired(0),
				eventRaised("bar"),
			},
		}
		err := o.deleteCancelledEventTimers(t.Context(), rs)
		require.NoError(t, err)
		assert.False(t, deleteCalled)
	})
}
