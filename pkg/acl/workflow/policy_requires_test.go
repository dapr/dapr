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
	"github.com/dapr/kit/ptr"
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

func req(eventType wfaclapi.RequiredEventType, status wfaclapi.RequiredStatus, name string) wfaclapi.RequiredEvent {
	return wfaclapi.RequiredEvent{EventType: eventType, Status: status, Name: name}
}

func reqWithAppID(eventType wfaclapi.RequiredEventType, status wfaclapi.RequiredStatus, name, appID string) wfaclapi.RequiredEvent {
	r := req(eventType, status, name)
	r.AppID = ptr.Of(appID)
	return r
}

func TestRequiresSatisfied_EmptyRequiresAlwaysTrue(t *testing.T) {
	assert.True(t, requiresSatisfied(nil, nil))
	assert.True(t, requiresSatisfied([]wfaclapi.RequiredEvent{}, historyFromChunks()))
}

func TestRequiresSatisfied_NoHistoryFailsClosed(t *testing.T) {
	requires := []wfaclapi.RequiredEvent{
		req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "A"),
	}
	assert.False(t, requiresSatisfied(requires, nil))
	assert.False(t, requiresSatisfied(requires, historyFromChunks()))
}

func TestRequiresSatisfied_ActivityStarted(t *testing.T) {
	history := historyFromChunks(
		chunkFromEvents(t, "app-a", taskScheduled(1, "FraudCheck")),
	)

	t.Run("matches when scheduled", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "FraudCheck"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("denies when name differs", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "OtherActivity"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})
}

func TestRequiresSatisfied_ActivityCompleted(t *testing.T) {
	history := historyFromChunks(
		chunkFromEvents(t, "app-a",
			taskScheduled(5, "FraudCheck"),
			taskCompleted(6, 5),
		),
	)

	t.Run("matches when scheduled+completed paired", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("denies when only scheduled (no completion event)", func(t *testing.T) {
		onlyScheduled := historyFromChunks(
			chunkFromEvents(t, "app-a", taskScheduled(5, "FraudCheck")),
		)
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck"),
		}
		assert.False(t, requiresSatisfied(requires, onlyScheduled))
	})

	t.Run("denies when completion's TaskScheduledId resolves to a different name", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "Other"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})
}

func TestRequiresSatisfied_WorkflowStarted(t *testing.T) {
	t.Run("matches ExecutionStarted by name", func(t *testing.T) {
		history := historyFromChunks(
			chunkFromEvents(t, "app-a", executionStarted(0, "OrderWF")),
		)
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusStarted, "OrderWF"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("matches ChildWorkflowInstanceCreated by name", func(t *testing.T) {
		history := historyFromChunks(
			chunkFromEvents(t, "app-a", childCreated(2, "SubOrderWF")),
		)
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusStarted, "SubOrderWF"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("denies when name differs", func(t *testing.T) {
		history := historyFromChunks(
			chunkFromEvents(t, "app-a", executionStarted(0, "OrderWF")),
		)
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusStarted, "OtherWF"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})
}

func TestRequiresSatisfied_WorkflowCompleted(t *testing.T) {
	t.Run("matches ChildWorkflowInstanceCompleted by paired creation name", func(t *testing.T) {
		history := historyFromChunks(
			chunkFromEvents(t, "app-a",
				childCreated(2, "SubOrderWF"),
				childCompleted(7, 2),
			),
		)
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusCompleted, "SubOrderWF"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("denies when only creation event (no completion)", func(t *testing.T) {
		history := historyFromChunks(
			chunkFromEvents(t, "app-a", childCreated(2, "SubOrderWF")),
		)
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeWorkflow, wfaclapi.RequiredStatusCompleted, "SubOrderWF"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})
}

func TestRequiresSatisfied_EventRaised(t *testing.T) {
	history := historyFromChunks(
		chunkFromEvents(t, "app-a", eventRaised(3, "ApprovalSignal")),
	)

	t.Run("matches when raised by name", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeEvent, wfaclapi.RequiredStatusRaised, "ApprovalSignal"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("denies when name differs", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeEvent, wfaclapi.RequiredStatusRaised, "OtherSignal"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})
}

func TestRequiresSatisfied_AppIDFilter(t *testing.T) {
	history := historyFromChunks(
		chunkFromEvents(t, "app-a", taskScheduled(1, "FraudCheck")),
		chunkFromEvents(t, "app-b", taskScheduled(1, "FraudCheck")),
	)

	t.Run("matches when chunk appID matches filter", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			reqWithAppID(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "FraudCheck", "app-b"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("denies when no chunk's appID matches filter", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			reqWithAppID(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "FraudCheck", "app-c"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})

	t.Run("nil AppID matches any chunk", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusStarted, "FraudCheck"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})
}

func TestRequiresSatisfied_MultipleRequiresAllMustMatch(t *testing.T) {
	history := historyFromChunks(
		chunkFromEvents(t, "app-a",
			taskScheduled(1, "FraudCheck"),
			taskCompleted(2, 1),
		),
	)

	t.Run("denies when any single requires entry is unmet", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck"),
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "HumanApprovalReceived"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})

	t.Run("allows only when every entry is met", func(t *testing.T) {
		fullHistory := historyFromChunks(
			chunkFromEvents(t, "app-a",
				taskScheduled(1, "FraudCheck"),
				taskCompleted(2, 1),
				taskScheduled(3, "HumanApprovalReceived"),
				taskCompleted(4, 3),
			),
		)
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck"),
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "HumanApprovalReceived"),
		}
		assert.True(t, requiresSatisfied(requires, fullHistory))
	})
}

func TestRequiresSatisfied_NoCollisionAcrossChunks(t *testing.T) {
	history := historyFromChunks(
		chunkFromEvents(t, "app-a",
			taskScheduled(1, "OtherActivity"),
		),
		chunkFromEvents(t, "app-b",
			taskScheduled(1, "FraudCheck"),
			taskCompleted(2, 1),
		),
	)

	t.Run("completion in chunk B resolves to chunk B's scheduled name", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck"),
		}
		assert.True(t, requiresSatisfied(requires, history))
	})

	t.Run("chunk A is not falsely credited with the completion from chunk B", func(t *testing.T) {
		requires := []wfaclapi.RequiredEvent{
			req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "OtherActivity"),
		}
		assert.False(t, requiresSatisfied(requires, history))
	})
}

func TestRequiresSatisfied_MalformedRawEventFailsClosed(t *testing.T) {
	history := &protos.PropagatedHistory{
		Chunks: []*protos.PropagatedHistoryChunk{{
			AppId:     "app-a",
			RawEvents: [][]byte{{0xFF, 0xFE, 0xFD}},
		}},
	}
	requires := []wfaclapi.RequiredEvent{
		req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck"),
	}
	assert.False(t, requiresSatisfied(requires, history))
}

func TestEvaluate_RequiresIntegratesWithRuleMatching(t *testing.T) {
	rule := wfaclapi.WorkflowAccessPolicyRule{
		Callers: []wfaclapi.WorkflowCaller{{AppID: "caller-app"}},
		Activities: []wfaclapi.ActivityRule{{
			Name: "ProcessPayment",
			Requires: []wfaclapi.RequiredEvent{
				req(wfaclapi.RequiredEventTypeActivity, wfaclapi.RequiredStatusCompleted, "FraudCheck"),
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
}
