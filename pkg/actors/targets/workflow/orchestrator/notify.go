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
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

const reminderNameParentNotify = "parent-notify"

// parentNotify is a completion notification ready to deliver. It is built
// under the turn lock so a detached delivery holds no reference to the state,
// which a recreate may reset while the call is in flight.
type parentNotify struct {
	msgs []*backend.WorkflowRuntimeStateMessage
	md   map[string][]string
	name string
}

func (o *orchestrator) newParentNotify(state *wfenginestate.State, msgs []*backend.WorkflowRuntimeStateMessage) parentNotify {
	started := o.getExecutionStartedEvent(state)
	// The parent drops a completion whose sender is not the child its
	// current generation created for the task, or whose parent execution is
	// not its current one (ids restart and may repeat across ContinueAsNew).
	md := map[string][]string{todo.MetadataSenderInstanceID: {o.actorID}}
	if pe := started.GetParentInstance().GetWorkflowInstance().GetExecutionId().GetValue(); pe != "" {
		md[todo.MetadataParentExecutionID] = []string{pe}
	}
	return parentNotify{msgs: msgs, md: md, name: started.GetName()}
}

// deliverParentNotify sends the notification, bounded so a lock cycle with a
// parent dispatching into this child resolves, and clears the marker once
// the parent acknowledged. On failure it arms the retry reminder, unless
// that reminder drove the send: its nack then retries under the scheduler's
// failure policy rather than an immediate re-arm.
func (o *orchestrator) deliverParentNotify(ctx context.Context, state *wfenginestate.State, pn parentNotify, arm bool) error {
	if len(pn.msgs) == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, escalateTimeout)
	defer cancel()
	res := o.messages.CallAddEventStateMessage(cctx, pn.msgs, pn.md)
	if res.Err == nil {
		// A crash before this save re-sends once on the refire; the parent
		// drops the duplicate.
		state.SetParentNotifyPending(false)
		if err := o.signAndSaveState(ctx, state); err != nil {
			return wferrors.NewRecoverable(fmt.Errorf("failed to clear the parent notification marker: %w", err))
		}
		return nil
	}
	if arm {
		if rerr := o.assertParentNotifyReminder(pn.name); rerr != nil {
			return wferrors.NewRecoverable(fmt.Errorf("failed to notify parent of completion: %w (and to arm the retry reminder: %v)", res.Err, rerr))
		}
	}
	return wferrors.NewRecoverable(fmt.Errorf("failed to notify parent of completion: %w", res.Err))
}

// rebuildParentNotify rebuilds the completion notification from durable
// history; empty when this workflow has no parent or has not completed.
func (o *orchestrator) rebuildParentNotify(ctx context.Context, state *wfenginestate.State) (parentNotify, error) {
	msg, err := o.parentNotification(ctx, state)
	if err != nil {
		return parentNotify{}, wferrors.NewRecoverable(fmt.Errorf("failed to rebuild the parent notification: %w", err))
	}
	if msg == nil {
		return parentNotify{}, nil
	}
	return o.newParentNotify(state, []*backend.WorkflowRuntimeStateMessage{msg}), nil
}

// resendParentNotification re-sends a pending notification under the lock;
// a failure nacks the driving reminder. arm adds the dedicated retry
// reminder when some other fire drove the re-send.
func (o *orchestrator) resendParentNotification(ctx context.Context, state *wfenginestate.State, arm bool) error {
	pn, err := o.rebuildParentNotify(ctx, state)
	if err != nil {
		return err
	}
	if len(pn.msgs) == 0 {
		// No parent or no completion to report: nothing is owed.
		state.SetParentNotifyPending(false)
		return o.signAndSaveState(ctx, state)
	}
	return o.deliverParentNotify(ctx, state, pn, arm)
}

// parentNotification mirrors the completion message the durabletask-go
// applier emits on the terminal turn, built from persisted history with the
// attestation attached. nil when this workflow has no parent or has not
// completed.
func (o *orchestrator) parentNotification(ctx context.Context, state *wfenginestate.State) (*backend.WorkflowRuntimeStateMessage, error) {
	started := o.getExecutionStartedEvent(state)
	parent := started.GetParentInstance()
	parentID := parent.GetWorkflowInstance().GetInstanceId()
	if parentID == "" {
		return nil, nil
	}
	var completed *protos.ExecutionCompletedEvent
	for i := len(state.History) - 1; i >= 0; i-- {
		if ec := state.History[i].GetExecutionCompleted(); ec != nil {
			completed = ec
			break
		}
	}
	if completed == nil {
		return nil, nil
	}

	targetApp := o.appID
	if parent.AppID != nil {
		targetApp = parent.GetAppID()
	}
	router := &protos.TaskRouter{SourceAppID: o.appID, TargetAppID: &targetApp}
	if parent.AppNamespace != nil {
		ns := parent.GetAppNamespace()
		router.TargetAppNamespace = &ns
	}
	evt := &backend.HistoryEvent{EventId: -1, Timestamp: timestamppb.Now(), Router: router}
	if completed.GetWorkflowStatus() == protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED {
		evt.EventType = &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
				TaskScheduledId: parent.GetTaskScheduledId(),
				Result:          completed.GetResult(),
			},
		}
	} else {
		evt.EventType = &protos.HistoryEvent_ChildWorkflowInstanceFailed{
			ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{
				TaskScheduledId: parent.GetTaskScheduledId(),
				FailureDetails:  completed.GetFailureDetails(),
			},
		}
	}
	if err := o.signing.AttachChildCompletionAttestation(ctx, evt, signing.ChildAttestationParams{
		ParentInstanceID:      parentID,
		ParentTaskScheduledID: parent.GetTaskScheduledId(),
		Input:                 attestationInput(state, started),
	}); err != nil {
		return nil, err
	}
	return &backend.WorkflowRuntimeStateMessage{HistoryEvent: evt, TargetInstanceId: parentID}, nil
}

// assertParentNotifyReminder arms the durable driver for a pending parent
// notification; the fixed name makes re-asserts idempotent. The turn context
// may already be cancelled (a notify parked behind the parent's lock past
// the local wake timeout), so the create runs on the actor's root context
// like an escalation.
func (o *orchestrator) assertParentNotifyReminder(workflowName string) error {
	ctx, cancel := context.WithTimeout(o.rootCtx, escalateTimeout)
	defer cancel()
	return o.createWorkflowReminderForever(ctx, reminderNameParentNotify, nil, time.Now(), o.appID, &workflowName)
}

// attestationInput is the input the parent verifies a child completion
// against: the one it created the child with. After a ContinueAsNew the start
// event carries the continued input, so the kept creation input wins.
func attestationInput(state *wfenginestate.State, started *protos.ExecutionStartedEvent) *wrapperspb.StringValue {
	if state.CreationInput != nil {
		return state.CreationInput
	}
	return started.GetInput()
}
