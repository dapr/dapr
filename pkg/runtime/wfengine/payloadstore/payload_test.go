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

package payloadstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
)

func TestPayloadReturnsFieldForEveryPayloadBearingEventType(t *testing.T) {
	t.Parallel()

	const marker = "payload-marker"

	for name, e := range map[string]*protos.HistoryEvent{
		"ExecutionStarted": {EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{Input: wrapperspb.String(marker)},
		}},
		"ExecutionCompleted": {EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{Result: wrapperspb.String(marker)},
		}},
		"ExecutionTerminated": {EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{Input: wrapperspb.String(marker)},
		}},
		"TaskScheduled": {EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Input: wrapperspb.String(marker)},
		}},
		"TaskCompleted": {EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{Result: wrapperspb.String(marker)},
		}},
		"ChildWorkflowInstanceCreated": {EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
			ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{Input: wrapperspb.String(marker)},
		}},
		"ChildWorkflowInstanceCompleted": {EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{Result: wrapperspb.String(marker)},
		}},
		"EventSent": {EventType: &protos.HistoryEvent_EventSent{
			EventSent: &protos.EventSentEvent{Input: wrapperspb.String(marker)},
		}},
		"EventRaised": {EventType: &protos.HistoryEvent_EventRaised{
			EventRaised: &protos.EventRaisedEvent{Input: wrapperspb.String(marker)},
		}},
		"ContinueAsNew": {EventType: &protos.HistoryEvent_ContinueAsNew{
			ContinueAsNew: &protos.ContinueAsNewEvent{Input: wrapperspb.String(marker)},
		}},
		"ExecutionSuspended": {EventType: &protos.HistoryEvent_ExecutionSuspended{
			ExecutionSuspended: &protos.ExecutionSuspendedEvent{Input: wrapperspb.String(marker)},
		}},
		"ExecutionResumed": {EventType: &protos.HistoryEvent_ExecutionResumed{
			ExecutionResumed: &protos.ExecutionResumedEvent{Input: wrapperspb.String(marker)},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := Payload(e)
			require.NotNil(t, p)
			assert.Equal(t, marker, p.GetValue())

			// Callers mutate the returned field in place; the event must
			// observe the change on the wire.
			p.Value = "mutated"
			data, err := proto.Marshal(e)
			require.NoError(t, err)
			assert.Contains(t, string(data), "mutated")
			assert.NotContains(t, string(data), marker)
		})
	}
}

func TestPayloadNilWhenAbsent(t *testing.T) {
	t.Parallel()

	for name, e := range map[string]*protos.HistoryEvent{
		"nil event":     nil,
		"no event type": {},
		"timer created": {EventType: &protos.HistoryEvent_TimerCreated{TimerCreated: &protos.TimerCreatedEvent{}}},
		"timer fired":   {EventType: &protos.HistoryEvent_TimerFired{TimerFired: &protos.TimerFiredEvent{}}},
		"workflow started": {EventType: &protos.HistoryEvent_WorkflowStarted{
			WorkflowStarted: &protos.WorkflowStartedEvent{},
		}},
		"task failed": {EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{FailureDetails: &protos.TaskFailureDetails{ErrorMessage: "boom"}},
		}},
		"child workflow failed": {EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{
			ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{},
		}},
		"payload field unset": {EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, Payload(e))
		})
	}
}

// TestPayloadAccessorCompleteness walks every HistoryEvent oneof variant
// via protoreflect and fails if a variant carries a StringValue field the
// accessor does not expose. This catches durabletask-go proto upgrades
// that add payload-bearing event types or fields: on failure, either add
// the field to Payload or, if it is not a user payload, to the allowlist
// below. Only direct fields are checked; nested messages (e.g.
// TaskFailureDetails.StackTrace) deliberately stay inline.
func TestPayloadAccessorCompleteness(t *testing.T) {
	t.Parallel()

	// StringValue fields that are not user payloads and must stay inline.
	nonPayloadFields := map[string]bool{
		"version":        true, // workflow definition version, small and system-set
		"workflowSpanID": true, // tracing correlation ID
	}

	const marker = "reflected-marker"

	desc := (&protos.HistoryEvent{}).ProtoReflect().Descriptor()
	oneof := desc.Oneofs().ByName("eventType")
	require.NotNil(t, oneof)

	fields := oneof.Fields()
	for i := range fields.Len() {
		variant := fields.Get(i)
		msgDesc := variant.Message()
		require.NotNil(t, msgDesc, "oneof variant %s is not a message", variant.Name())

		subfields := msgDesc.Fields()
		for j := range subfields.Len() {
			sub := subfields.Get(j)
			if sub.Kind() != protoreflect.MessageKind || sub.Message().FullName() != "google.protobuf.StringValue" {
				continue
			}
			if nonPayloadFields[string(sub.Name())] {
				continue
			}

			// Build a HistoryEvent of this variant with the field set.
			e := &protos.HistoryEvent{}
			er := e.ProtoReflect()
			vmsg := er.NewField(variant).Message()
			sv := vmsg.NewField(sub).Message()
			sv.Set(sv.Descriptor().Fields().ByName("value"), protoreflect.ValueOfString(marker))
			vmsg.Set(sub, protoreflect.ValueOfMessage(sv))
			er.Set(variant, protoreflect.ValueOfMessage(vmsg))

			p := Payload(e)
			require.NotNilf(t, p,
				"HistoryEvent variant %q has StringValue field %q that the Payload accessor does not expose; add it to Payload or to the non-payload allowlist",
				variant.Name(), sub.Name())
			assert.Equal(t, marker, p.GetValue())
		}
	}
}
