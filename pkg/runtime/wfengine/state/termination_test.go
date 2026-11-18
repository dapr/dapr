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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func terminationEvent(input *wrapperspb.StringValue) *protos.HistoryEvent {
	return &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{Input: input},
		},
	}
}

func terminatedRuntimeState(oldEvents, newEvents []*protos.HistoryEvent, customStatus *wrapperspb.StringValue) *backend.WorkflowRuntimeState {
	return &backend.WorkflowRuntimeState{
		CustomStatus: customStatus,
		CompletedEvent: &protos.ExecutionCompletedEvent{
			WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED,
		},
		OldEvents: oldEvents,
		NewEvents: newEvents,
	}
}

func TestCustomStatusUsesTerminationInput(t *testing.T) {
	customStatus := wrapperspb.String("workflow progress")
	reason := wrapperspb.String("cancelled by operator")
	state := terminatedRuntimeState([]*protos.HistoryEvent{terminationEvent(reason)}, nil, customStatus)

	require.Same(t, reason, CustomStatus(state))
}

func TestCustomStatusPreservesWorkflowStatusWithoutTerminationInput(t *testing.T) {
	customStatus := wrapperspb.String("workflow progress")
	state := terminatedRuntimeState([]*protos.HistoryEvent{terminationEvent(nil)}, nil, customStatus)

	require.Same(t, customStatus, CustomStatus(state))
}

func TestCustomStatusPreservesExplicitEmptyTerminationInput(t *testing.T) {
	reason := wrapperspb.String("")
	state := terminatedRuntimeState(nil, []*protos.HistoryEvent{terminationEvent(reason)}, wrapperspb.String("workflow progress"))

	got := CustomStatus(state)
	require.NotNil(t, got)
	assert.Empty(t, got.GetValue())
}

func TestCustomStatusUsesFirstTerminationEventInHistoryOrder(t *testing.T) {
	first := wrapperspb.String("first")
	second := wrapperspb.String("second")
	state := terminatedRuntimeState(
		[]*protos.HistoryEvent{terminationEvent(first)},
		[]*protos.HistoryEvent{terminationEvent(second)},
		nil,
	)

	require.Same(t, first, CustomStatus(state))
}

func TestCustomStatusKeepsFirstTerminationWithoutInput(t *testing.T) {
	customStatus := wrapperspb.String("workflow progress")
	state := terminatedRuntimeState(
		[]*protos.HistoryEvent{
			terminationEvent(nil),
			terminationEvent(wrapperspb.String("later reason")),
		},
		nil,
		customStatus,
	)

	require.Same(t, customStatus, CustomStatus(state))
}

func TestCustomStatusDoesNotExposePendingTerminationInput(t *testing.T) {
	customStatus := wrapperspb.String("workflow progress")
	state := &backend.WorkflowRuntimeState{
		CustomStatus: customStatus,
		StartEvent:   &protos.ExecutionStartedEvent{Name: "test"},
		NewEvents:    []*protos.HistoryEvent{terminationEvent(wrapperspb.String("pending reason"))},
	}

	require.Same(t, customStatus, CustomStatus(state))
}

func TestCustomStatusNilState(t *testing.T) {
	assert.Nil(t, CustomStatus(nil))
	assert.Nil(t, TerminationInput(nil))
}
