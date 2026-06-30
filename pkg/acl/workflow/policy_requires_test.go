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

package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/durabletask-go/api/protos"
)

func marshalEvent(t *testing.T, e *protos.HistoryEvent) []byte {
	t.Helper()
	b, err := proto.Marshal(e)
	require.NoError(t, err)
	return b
}

func chunkFromEvents(t *testing.T, appID string, events ...*protos.HistoryEvent) *protos.PropagatedHistoryChunk {
	t.Helper()
	chunk := &protos.PropagatedHistoryChunk{AppId: appID}
	for _, e := range events {
		chunk.RawEvents = append(chunk.RawEvents, marshalEvent(t, e))
	}
	return chunk
}

func historyFromChunks(chunks ...*protos.PropagatedHistoryChunk) *protos.PropagatedHistory {
	return &protos.PropagatedHistory{Chunks: chunks}
}

func decode(t *testing.T, history *protos.PropagatedHistory) []decodedChunk {
	t.Helper()
	chunks, ok := decodeHistoryChunks(history)
	require.True(t, ok)
	return chunks
}

func taskScheduled(eventID int32, name string) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventId: eventID,
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: name},
		},
	}
}

func taskCompleted(eventID, scheduledID int32) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventId: eventID,
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: scheduledID},
		},
	}
}

func executionStarted(eventID int32, name string) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventId: eventID,
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{Name: name},
		},
	}
}

func childCreated(eventID int32, name string) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventId: eventID,
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
			ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{Name: name},
		},
	}
}

func childCompleted(eventID, scheduledID int32) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventId: eventID,
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: scheduledID},
		},
	}
}

func eventRaised(eventID int32, name string) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventId: eventID,
		EventType: &protos.HistoryEvent_EventRaised{
			EventRaised: &protos.EventRaisedEvent{Name: name},
		},
	}
}

func req(eventType wfaclapi.RequiredEventType, status wfaclapi.RequiredStatus, name, appID string) wfaclapi.RequiredEvent {
	return wfaclapi.RequiredEvent{EventType: eventType, Status: status, Name: name, AppID: appID}
}

func TestRequiresMatchChunks_EmptyRequiresAlwaysTrue(t *testing.T) {
	assert.True(t, requiresMatchChunks(nil, nil))
	assert.True(t, requiresMatchChunks([]wfaclapi.RequiredEvent{}, nil))
}

func TestRequiresMatchChunks_NoChunksDenies(t *testing.T) {
	requires := []wfaclapi.RequiredEvent{
		req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "A", "app-a"),
	}
	assert.False(t, requiresMatchChunks(requires, nil))
	assert.False(t, requiresMatchChunks(requires, []decodedChunk{}))
}

func TestRequiresMatchChunks_ActivityStarted(t *testing.T) {
	chunks := decode(t, historyFromChunks(
		chunkFromEvents(t, "app-a", taskScheduled(1, "FraudCheck")),
	))

	t.Run("matches when scheduled", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "FraudCheck", "app-a"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("denies when name differs", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "OtherActivity", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})
}

func TestRequiresMatchChunks_ActivityCompleted(t *testing.T) {
	chunks := decode(t, historyFromChunks(
		chunkFromEvents(t, "app-a",
			taskScheduled(5, "FraudCheck"),
			taskCompleted(6, 5),
		),
	))

	t.Run("matches when scheduled+completed paired", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "app-a"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("denies when only scheduled (no completion event)", func(t *testing.T) {
		onlyScheduled := decode(t, historyFromChunks(
			chunkFromEvents(t, "app-a", taskScheduled(5, "FraudCheck")),
		))
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, onlyScheduled))
	})

	t.Run("denies when completion's TaskScheduledId resolves to a different name", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "Other", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})
}

func TestRequiresMatchChunks_WorkflowStarted(t *testing.T) {
	t.Run("matches ExecutionStarted by name", func(t *testing.T) {
		chunks := decode(t, historyFromChunks(
			chunkFromEvents(t, "app-a", executionStarted(0, "OrderWF")),
		))
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusStarted, "OrderWF", "app-a"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("matches ChildWorkflowInstanceCreated by name", func(t *testing.T) {
		chunks := decode(t, historyFromChunks(
			chunkFromEvents(t, "app-a", childCreated(2, "SubOrderWF")),
		))
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusStarted, "SubOrderWF", "app-a"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("denies when name differs", func(t *testing.T) {
		chunks := decode(t, historyFromChunks(
			chunkFromEvents(t, "app-a", executionStarted(0, "OrderWF")),
		))
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusStarted, "OtherWF", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})
}

func TestRequiresMatchChunks_WorkflowCompleted(t *testing.T) {
	t.Run("matches ChildWorkflowInstanceCompleted by paired creation name", func(t *testing.T) {
		chunks := decode(t, historyFromChunks(
			chunkFromEvents(t, "app-a",
				childCreated(2, "SubOrderWF"),
				childCompleted(7, 2),
			),
		))
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusCompleted, "SubOrderWF", "app-a"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("denies when only creation event (no completion)", func(t *testing.T) {
		chunks := decode(t, historyFromChunks(
			chunkFromEvents(t, "app-a", childCreated(2, "SubOrderWF")),
		))
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusCompleted, "SubOrderWF", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})
}

func TestRequiresMatchChunks_EventRaised(t *testing.T) {
	chunks := decode(t, historyFromChunks(
		chunkFromEvents(t, "app-a", eventRaised(3, "ApprovalSignal")),
	))

	t.Run("matches when raised by name", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeEvent, wfaclapi.RequiredStatusRaised, "ApprovalSignal", "app-a"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("denies when name differs", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeEvent, wfaclapi.RequiredStatusRaised, "OtherSignal", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})
}

func TestRequiresMatchChunks_AppIDFilter(t *testing.T) {
	chunks := decode(t, historyFromChunks(
		chunkFromEvents(t, "app-a", taskScheduled(1, "FraudCheck")),
		chunkFromEvents(t, "app-b", taskScheduled(1, "FraudCheck")),
	))

	t.Run("matches when chunk appID matches filter", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "FraudCheck", "app-b"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("denies when no chunk's appID matches filter", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "FraudCheck", "app-c"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})
}

func TestRequiresMatchChunks_MultipleRequiresAllMustMatch(t *testing.T) {
	chunks := decode(t, historyFromChunks(
		chunkFromEvents(t, "app-a",
			taskScheduled(1, "FraudCheck"),
			taskCompleted(2, 1),
		),
	))

	t.Run("denies when any single requires entry is unmet", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "app-a"),
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "HumanApprovalReceived", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("allows only when every entry is met", func(t *testing.T) {
		fullChunks := decode(t, historyFromChunks(
			chunkFromEvents(t, "app-a",
				taskScheduled(1, "FraudCheck"),
				taskCompleted(2, 1),
				taskScheduled(3, "HumanApprovalReceived"),
				taskCompleted(4, 3),
			),
		))
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "app-a"),
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "HumanApprovalReceived", "app-a"),
		}
		assert.True(t, requiresMatchChunks(requires, fullChunks))
	})
}

// Regression: chunks from different apps can share event IDs.
func TestRequiresMatchChunks_NoCollisionAcrossChunks(t *testing.T) {
	chunks := decode(t, historyFromChunks(
		chunkFromEvents(t, "app-a",
			taskScheduled(1, "OtherActivity"),
		),
		chunkFromEvents(t, "app-b",
			taskScheduled(1, "FraudCheck"),
			taskCompleted(2, 1),
		),
	))

	t.Run("completion in chunk B resolves to chunk B's scheduled name", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "app-b"),
		}
		assert.True(t, requiresMatchChunks(requires, chunks))
	})

	t.Run("chunk A is not falsely credited with the completion from chunk B", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "OtherActivity", "app-a"),
		}
		assert.False(t, requiresMatchChunks(requires, chunks))
	})
}

func TestDecodeHistoryChunks_MalformedRawEventFailsClosed(t *testing.T) {
	history := &protos.PropagatedHistory{
		Chunks: []*protos.PropagatedHistoryChunk{{
			AppId:     "app-a",
			RawEvents: [][]byte{{0xFF, 0xFE, 0xFD}},
		}},
	}
	_, ok := decodeHistoryChunks(history)
	assert.False(t, ok)
}

func TestDecodeHistoryChunks_NilAndEmpty(t *testing.T) {
	chunks, ok := decodeHistoryChunks(nil)
	assert.True(t, ok)
	assert.Empty(t, chunks)

	chunks, ok = decodeHistoryChunks(&protos.PropagatedHistory{})
	assert.True(t, ok)
	assert.Empty(t, chunks)
}

func TestEvaluate_RequiresIntegratesWithRuleMatching(t *testing.T) {
	rule := wfaclapi.WorkflowAccessPolicyRule{
		Callers: []wfaclapi.WorkflowCaller{{AppID: "caller-app"}},
		Activities: []wfaclapi.ActivityRule{{
			Name: "ProcessPayment",
			Requires: []wfaclapi.RequiredEvent{
				req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "caller-app"),
			},
		}},
	}
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		{Spec: wfaclapi.WorkflowAccessPolicySpec{Rules: []wfaclapi.WorkflowAccessPolicyRule{rule}}},
	})

	t.Run("allowed when requires are satisfied", func(t *testing.T) {
		history := historyFromChunks(
			chunkFromEvents(t, "caller-app",
				taskScheduled(1, "FraudCheck"),
				taskCompleted(2, 1),
			),
		)
		allowed, reason := cp.Evaluate("caller-app", OperationTypeActivity, wfaclapi.WorkflowOperationSchedule, "ProcessPayment", history)
		assert.True(t, allowed)
		assert.Equal(t, DenialReasonNone, reason)
	})

	t.Run("denied with RequiresUnmet when caller+name match but history is missing", func(t *testing.T) {
		allowed, reason := cp.Evaluate("caller-app", OperationTypeActivity, wfaclapi.WorkflowOperationSchedule, "ProcessPayment", nil)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonRequiresUnmet, reason)
	})

	t.Run("denied with NotAllowed when no rule matches caller", func(t *testing.T) {
		allowed, reason := cp.Evaluate("other-app", OperationTypeActivity, wfaclapi.WorkflowOperationSchedule, "ProcessPayment", nil)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonNotAllowed, reason)
	})

	t.Run("malformed propagated history denies requires-bearing rule", func(t *testing.T) {
		bad := &protos.PropagatedHistory{Chunks: []*protos.PropagatedHistoryChunk{{
			AppId:     "caller-app",
			RawEvents: [][]byte{{0xFF, 0xFE, 0xFD}},
		}}}
		allowed, reason := cp.Evaluate("caller-app", OperationTypeActivity, wfaclapi.WorkflowOperationSchedule, "ProcessPayment", bad)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonRequiresUnmet, reason)
	})
}

// when the matching rule has no requires, Evaluate never
// touches the propagated history. Malformed bytes that would fail decode
// must not affect the result.
func TestEvaluate_SkipsDecodeWhenMatchingRuleHasNoRequires(t *testing.T) {
	// Two rules in the same policy: a requires-bearing one for "SensitiveAct"
	// and a no-requires one for "PlainAct". Scheduling PlainAct must not
	// trigger a decode of the propagated chunks.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{Spec: wfaclapi.WorkflowAccessPolicySpec{
		Rules: []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "caller-app"}},
			Activities: []wfaclapi.ActivityRule{
				{Name: "SensitiveAct", Requires: []wfaclapi.RequiredEvent{
					req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "caller-app"),
				}},
				{Name: "PlainAct"},
			},
		}},
	}}})

	bad := &protos.PropagatedHistory{Chunks: []*protos.PropagatedHistoryChunk{{
		AppId:     "caller-app",
		RawEvents: [][]byte{{0xFF, 0xFE, 0xFD}},
	}}}
	allowed, reason := cp.Evaluate("caller-app", OperationTypeActivity, wfaclapi.WorkflowOperationSchedule, "PlainAct", bad)
	assert.True(t, allowed)
	assert.Equal(t, DenialReasonNone, reason)
}

// Multiple schedule rules with the same workflow name but different requires
// compose as OR: access is allowed if any matching rule's requires is satisfied.
func TestEvaluate_MultipleScheduleEntriesOR(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{Spec: wfaclapi.WorkflowAccessPolicySpec{
		Rules: []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "caller-app"}},
			Workflows: []wfaclapi.WorkflowRule{
				{
					Name:       "GatedWF",
					Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperationSchedule},
					Requires: []wfaclapi.RequiredEvent{
						req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "PathA", "caller-app"),
					},
				},
				{
					Name:       "GatedWF",
					Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperationSchedule},
					Requires: []wfaclapi.RequiredEvent{
						req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "PathB", "caller-app"),
					},
				},
			},
		}},
	}}})

	t.Run("allowed when first entry's requires is satisfied", func(t *testing.T) {
		history := historyFromChunks(chunkFromEvents(t, "caller-app",
			taskScheduled(1, "PathA"), taskCompleted(2, 1),
		))
		allowed, reason := cp.Evaluate("caller-app", OperationTypeWorkflow, wfaclapi.WorkflowOperationSchedule, "GatedWF", history)
		assert.True(t, allowed)
		assert.Equal(t, DenialReasonNone, reason)
	})

	t.Run("allowed when second entry's requires is satisfied", func(t *testing.T) {
		history := historyFromChunks(chunkFromEvents(t, "caller-app",
			taskScheduled(1, "PathB"), taskCompleted(2, 1),
		))
		allowed, reason := cp.Evaluate("caller-app", OperationTypeWorkflow, wfaclapi.WorkflowOperationSchedule, "GatedWF", history)
		assert.True(t, allowed)
		assert.Equal(t, DenialReasonNone, reason)
	})

	t.Run("denied when neither entry's requires is satisfied", func(t *testing.T) {
		history := historyFromChunks(chunkFromEvents(t, "caller-app",
			taskScheduled(1, "Unrelated"), taskCompleted(2, 1),
		))
		allowed, reason := cp.Evaluate("caller-app", OperationTypeWorkflow, wfaclapi.WorkflowOperationSchedule, "GatedWF", history)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonRequiresUnmet, reason)
	})
}

// AND nested within OR: two rules for the same workflow compose as OR, and the
// first rule's multi-event requires list composes as AND. Access is granted by
// "(FraudCheck AND HumanApproval) OR VipVerified". The AND branch must be
// fully satisfied — partially satisfying it must not grant access.
func TestEvaluate_AndWithinOr(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{Spec: wfaclapi.WorkflowAccessPolicySpec{
		Rules: []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "caller-app"}},
			Workflows: []wfaclapi.WorkflowRule{
				{
					Name:       "GatedWF",
					Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperationSchedule},
					Requires: []wfaclapi.RequiredEvent{
						req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck", "caller-app"),
						req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "HumanApproval", "caller-app"),
					},
				},
				{
					Name:       "GatedWF",
					Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperationSchedule},
					Requires: []wfaclapi.RequiredEvent{
						req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "VipVerified", "caller-app"),
					},
				},
			},
		}},
	}}})

	t.Run("allowed when the AND branch is fully satisfied", func(t *testing.T) {
		history := historyFromChunks(chunkFromEvents(t, "caller-app",
			taskScheduled(1, "FraudCheck"), taskCompleted(2, 1),
			taskScheduled(3, "HumanApproval"), taskCompleted(4, 3),
		))
		allowed, reason := cp.Evaluate("caller-app", OperationTypeWorkflow, wfaclapi.WorkflowOperationSchedule, "GatedWF", history)
		assert.True(t, allowed)
		assert.Equal(t, DenialReasonNone, reason)
	})

	t.Run("allowed when the OR alternative is satisfied", func(t *testing.T) {
		history := historyFromChunks(chunkFromEvents(t, "caller-app",
			taskScheduled(1, "VipVerified"), taskCompleted(2, 1),
		))
		allowed, reason := cp.Evaluate("caller-app", OperationTypeWorkflow, wfaclapi.WorkflowOperationSchedule, "GatedWF", history)
		assert.True(t, allowed)
		assert.Equal(t, DenialReasonNone, reason)
	})

	t.Run("denied when the AND branch is only partially satisfied", func(t *testing.T) {
		history := historyFromChunks(chunkFromEvents(t, "caller-app",
			taskScheduled(1, "FraudCheck"), taskCompleted(2, 1),
		))
		allowed, reason := cp.Evaluate("caller-app", OperationTypeWorkflow, wfaclapi.WorkflowOperationSchedule, "GatedWF", history)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonRequiresUnmet, reason)
	})

	t.Run("denied when no branch is satisfied", func(t *testing.T) {
		history := historyFromChunks(chunkFromEvents(t, "caller-app",
			taskScheduled(1, "Unrelated"), taskCompleted(2, 1),
		))
		allowed, reason := cp.Evaluate("caller-app", OperationTypeWorkflow, wfaclapi.WorkflowOperationSchedule, "GatedWF", history)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonRequiresUnmet, reason)
	})
}
