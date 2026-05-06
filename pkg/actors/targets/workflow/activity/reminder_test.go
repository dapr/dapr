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

package activity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Test_ReminderPayload_PreservesPropagation verifies that the reminder data
// round-trip preserves the full ActivityInvocation, including the propagated
// history
func Test_ReminderPayload_PreservesPropagation(t *testing.T) {
	original := &protos.ActivityInvocation{
		HistoryEvent: &protos.HistoryEvent{
			EventId: 7,
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{
					Name:  "CallLLM",
					Input: wrapperspb.String(`{"prompt":"hi"}`),
				},
			},
		},
		PropagatedHistory: &protos.PropagatedHistory{
			Scope: protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_LINEAGE,
			Events: []*protos.HistoryEvent{
				{EventId: 1, EventType: &protos.HistoryEvent_ExecutionStarted{
					ExecutionStarted: &protos.ExecutionStartedEvent{Name: "parentWf"},
				}},
				{EventId: 2, EventType: &protos.HistoryEvent_TaskScheduled{
					TaskScheduled: &protos.TaskScheduledEvent{Name: "someEarlierActivity"},
				}},
			},
			Chunks: []*protos.PropagatedHistoryChunk{
				{AppId: "app0", InstanceId: "wf-1", WorkflowName: "parentWf", StartEventIndex: 0, EventCount: 2},
			},
		},
	}

	// Simulate createReminder: marshal the ActivityInvocation
	data, err := anypb.New(original)
	require.NoError(t, err, "marshal to anypb.Any should succeed")

	// Simulate handleReminder: unmarshal from reminder.Data back into a
	// fresh ActivityInvocation and verify all propagation fields
	var decoded protos.ActivityInvocation
	require.NoError(t, data.UnmarshalTo(&decoded), "unmarshal from anypb.Any should succeed")

	require.NotNil(t, decoded.GetHistoryEvent(), "HistoryEvent should survive reminder round-trip")
	assert.Equal(t, int32(7), decoded.GetHistoryEvent().GetEventId())
	assert.Equal(t, "CallLLM", decoded.GetHistoryEvent().GetTaskScheduled().GetName())
	ph := decoded.GetPropagatedHistory()
	require.NotNil(t, ph, "PropagatedHistory should survive reminder round-trip (durability guarantee)")
	assert.Equal(t, protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_LINEAGE, ph.GetScope())
	require.Len(t, ph.GetEvents(), 2, "propagated events should be preserved")
	require.Len(t, ph.GetChunks(), 1, "propagated chunks should be preserved")
	assert.Equal(t, "app0", ph.GetChunks()[0].GetAppId())
	assert.Equal(t, "parentWf", ph.GetChunks()[0].GetWorkflowName())
}

// Test_DecodeActivityInvocation_LegacyHistoryEventPayload verifies backward
// compatibility: activity payloads marshaled by pre-propagation orchestrators
// contain a raw HistoryEvent (no envelope, no PropagatedHistory). The new
// receiver code must still decode these without error after a rolling
// upgrade. Output: an ActivityInvocation with the HistoryEvent filled in and
// nil PropagatedHistory.
func Test_DecodeActivityInvocation_LegacyHistoryEventPayload(t *testing.T) {
	// Simulate what an OLD orchestrator would have sent: just a raw
	// HistoryEvent proto, no ActivityInvocation envelope.
	legacyEvent := &backend.HistoryEvent{
		EventId: 5,
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{
				Name:  "legacyActivity",
				Input: wrapperspb.String(`{"hello":"world"}`),
			},
		},
	}
	data, err := proto.Marshal(legacyEvent)
	require.NoError(t, err)

	// Try the new ActivityInvocation envelope format first. Fall back to
	// the legacy raw HistoryEvent payload for reminders created by
	// pre-propagation code
	invocation, _, err := decodeActivityInvocation(data)
	require.NoError(t, err, "should decode legacy HistoryEvent payload without error")
	require.NotNil(t, invocation)
	require.NotNil(t, invocation.GetHistoryEvent())
	assert.Equal(t, int32(5), invocation.GetHistoryEvent().GetEventId())
	assert.Equal(t, "legacyActivity", invocation.GetHistoryEvent().GetTaskScheduled().GetName())
	assert.Nil(t, invocation.GetPropagatedHistory(), "legacy payloads have no propagation")
}

// Test_DecodeActivityInvocation_NewEnvelopePayload verifies the happy path —
// a new ActivityInvocation envelope round-trips correctly and preserves
// both HistoryEvent & PropagatedHistory.
func Test_DecodeActivityInvocation_NewEnvelopePayload(t *testing.T) {
	original := &protos.ActivityInvocation{
		HistoryEvent: &protos.HistoryEvent{
			EventId: 9,
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{Name: "newActivity"},
			},
		},
		PropagatedHistory: &protos.PropagatedHistory{
			Scope: protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_LINEAGE,
		},
	}
	data, err := proto.Marshal(original)
	require.NoError(t, err)

	// Try the new ActivityInvocation envelope format first. Fall back to
	// the legacy raw HistoryEvent payload for reminders created by
	// pre-propagation code
	invocation, _, err := decodeActivityInvocation(data)
	require.NoError(t, err)
	require.NotNil(t, invocation.GetHistoryEvent())
	assert.Equal(t, "newActivity", invocation.GetHistoryEvent().GetTaskScheduled().GetName())
	require.NotNil(t, invocation.GetPropagatedHistory())
	assert.Equal(t, protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_LINEAGE, invocation.GetPropagatedHistory().GetScope())
}

// Test_ReminderData_LegacyHistoryEventPayload verifies the reminder
// round-trip fallback
func Test_ReminderData_LegacyHistoryEventPayload(t *testing.T) {
	legacyEvent := &backend.HistoryEvent{
		EventId: 11,
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "staleActivity"},
		},
	}
	// Simulate what pre-propagation createReminder would have written to
	// reminder.Data: anypb.Any wrapping a HistoryEvent (not ActivityInvocation).
	legacyData, err := anypb.New(legacyEvent)
	require.NoError(t, err)
	var invocation protos.ActivityInvocation
	primaryErr := legacyData.UnmarshalTo(&invocation)
	require.Error(t, primaryErr, "decoding legacy payload as ActivityInvocation must fail so the fallback triggers")

	// Fallback path: unmarshal as HistoryEvent.
	var event backend.HistoryEvent
	require.NoError(t, legacyData.UnmarshalTo(&event), "fallback to HistoryEvent must succeed")
	assert.Equal(t, int32(11), event.GetEventId())
	assert.Equal(t, "staleActivity", event.GetTaskScheduled().GetName())
}

// Test_ReminderPayload_NilPropagation verifies that an ActivityInvocation
// with no propagation still round-trips correctly — must not
// panic/corrupt the HistoryEvent.
func Test_ReminderPayload_NilPropagation(t *testing.T) {
	// PropagatedHistory is nil — activity was called without propagation.
	original := &protos.ActivityInvocation{
		HistoryEvent: &protos.HistoryEvent{
			EventId: 3,
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{Name: "plainActivity"},
			},
		},
	}

	data, err := anypb.New(original)
	require.NoError(t, err)

	var decoded protos.ActivityInvocation
	require.NoError(t, data.UnmarshalTo(&decoded))

	require.NotNil(t, decoded.GetHistoryEvent())
	assert.Equal(t, "plainActivity", decoded.GetHistoryEvent().GetTaskScheduled().GetName())
	assert.Nil(t, decoded.GetPropagatedHistory(), "nil propagation should remain nil after round-trip")
}
