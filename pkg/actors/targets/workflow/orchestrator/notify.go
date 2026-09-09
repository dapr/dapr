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

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
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

// deliverParentNotify delivers off the turn lock: a parent turn may be
// dispatching into this child at the same time. On ack the marker is cleared
// under the lock; on failure the retry reminder is armed. Reports whether a
// delivery is in flight so the caller keeps the actor resident for the clear.
func (o *orchestrator) deliverParentNotify(pn parentNotify) bool {
	if len(pn.msgs) == 0 {
		return false
	}
	if !o.parentNotifyInFlight.CompareAndSwap(false, true) {
		return true
	}
	started := o.detached.Go(func(rootCtx context.Context) {
		defer o.parentNotifyInFlight.Store(false)
		if err := o.deliverParentNotifySync(rootCtx, pn); err != nil {
			log.Debugf("Workflow actor '%s': %v; the retry reminder re-sends", o.actorID, err)
			o.armParentNotifyRetry(pn.name)
		}
	})
	if !started {
		o.parentNotifyInFlight.Store(false)
		return false
	}
	return true
}

// deliverParentNotifySync sends the notification, bounded, and clears the
// marker on ack. The caller must not hold the turn lock.
func (o *orchestrator) deliverParentNotifySync(ctx context.Context, pn parentNotify) error {
	cctx, cancel := context.WithTimeout(ctx, escalateTimeout)
	defer cancel()
	if res := o.messages.CallAddEventStateMessage(cctx, pn.msgs, pn.md); res.Err != nil {
		return wferrors.NewRecoverable(fmt.Errorf("failed to notify parent of completion: %w", res.Err))
	}
	if err := o.clearParentNotify(cctx); err != nil {
		return wferrors.NewRecoverable(fmt.Errorf("parent acknowledged but the marker could not be cleared: %w", err))
	}
	return nil
}

// resendParentNotification drives the retry reminder: the notification is
// rebuilt under the lock and delivered off it; a failure nacks the fire, so
// the scheduler's failure policy retries and the reminder stays the driver.
func (o *orchestrator) resendParentNotification(ctx context.Context) error {
	unlock, err := o.contextLockMeasured(ctx, "reminder")
	if err != nil {
		return err
	}
	pn, err := func() (parentNotify, error) {
		defer unlock()
		state, _, lerr := o.loadInternalState(ctx)
		if lerr != nil || state == nil || !state.ParentNotifyPending || !runtimestate.IsCompleted(o.rstate) {
			return parentNotify{}, lerr
		}
		return o.pendingParentNotify(ctx, state)
	}()
	if err != nil || len(pn.msgs) == 0 {
		return err
	}
	return o.deliverParentNotifySync(ctx, pn)
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

// clearParentNotify clears the marker under the lock; a recreate or purge
// leaves nothing to clear.
func (o *orchestrator) clearParentNotify(ctx context.Context) error {
	unlock, err := o.lock.ContextLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil || !state.ParentNotifyPending {
		return nil
	}
	state.SetParentNotifyPending(false)
	return o.signAndSaveState(ctx, state)
}

// armParentNotifyRetry arms the retry reminder with the failure policy's
// jittered delay.
func (o *orchestrator) armParentNotifyRetry(workflowName string) {
	delay := common.NewJitterBackoff(common.RetryBackoffBase, common.RetryBackoffCap).NextBackOff()
	if err := o.assertParentNotifyReminder(workflowName, time.Now().Add(delay)); err != nil {
		log.Warnf("Workflow actor '%s': failed to arm the parent notification retry reminder; the janitor re-sends: %v", o.actorID, err)
	}
}

// pendingParentNotify rebuilds the pending notification under the lock;
// when nothing is owed the marker is cleared in place.
func (o *orchestrator) pendingParentNotify(ctx context.Context, state *wfenginestate.State) (parentNotify, error) {
	pn, err := o.rebuildParentNotify(ctx, state)
	if err != nil {
		return parentNotify{}, err
	}
	if len(pn.msgs) == 0 {
		state.SetParentNotifyPending(false)
		return parentNotify{}, o.signAndSaveState(ctx, state)
	}
	return pn, nil
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
func (o *orchestrator) assertParentNotifyReminder(workflowName string, due time.Time) error {
	ctx, cancel := context.WithTimeout(o.rootCtx, escalateTimeout)
	defer cancel()
	return o.createWorkflowReminderForever(ctx, reminderNameParentNotify, nil, due, o.appID, &workflowName)
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
