/*
Copyright 2025 The Dapr Authors
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
	"fmt"
	"net/http"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

func (o *orchestrator) handleInvoke(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	if req.GetMessage() == nil {
		return nil, errors.New("message is nil in request")
	}

	method := req.GetMessage().GetMethod()
	data := req.GetMessage().GetData().GetValue()

	// AddWorkflowEvent encodes the operation in its HistoryEvent payload, and
	// the orchestrator processes the same parsed event for its own logic.
	// Unmarshal once here, share with both the access policy check and
	// addWorkflowEvent.
	var parsedAddEvent *backend.HistoryEvent
	if method == todo.AddWorkflowEventMethod {
		var ev backend.HistoryEvent
		if err := proto.Unmarshal(data, &ev); err != nil {
			return nil, fmt.Errorf("failed to unmarshal AddWorkflowEvent HistoryEvent: %w", err)
		}
		parsedAddEvent = &ev
	}

	if err := o.checkAccessPolicy(ctx, method, data, parsedAddEvent, nil, req.GetMetadata()); err != nil {
		return nil, err
	}

	// Create the InvokeMethodRequest
	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	policyDef := o.resiliency.ActorPostLockPolicy(o.actorType, o.actorID)
	policyRunner := resiliency.NewRunner[*internalsv1pb.InternalInvokeResponse](ctx, policyDef)
	msg := imReq.Message()
	return policyRunner(func(ctx context.Context) (*internalsv1pb.InternalInvokeResponse, error) {
		resData, err := o.executeMethod(ctx, msg.GetMethod(), req.GetMetadata(), msg.GetData().GetValue(), parsedAddEvent)
		if err != nil {
			return nil, err
		}

		return &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{
				Code: http.StatusOK,
			},
			Message: &commonv1pb.InvokeResponse{
				Data: &anypb.Any{
					Value: resData,
				},
			},
		}, nil
	})
}

func (o *orchestrator) executeMethod(ctx context.Context, methodName string, meta map[string]*internalsv1pb.ListStringValue, request []byte, parsedAddEvent *backend.HistoryEvent) ([]byte, error) {
	log.Debugf("Workflow actor '%s': invoking method '%s'", o.actorID, methodName)

	if o.actorState == nil {
		return nil, messages.ErrActorRuntimeNotFound
	}

	switch methodName {
	case todo.CreateWorkflowInstanceMethod:
		return nil, o.createWorkflowInstance(ctx, request)

	case todo.AddWorkflowEventMethod:
		return nil, o.addWorkflowEvent(ctx, parsedAddEvent, senderFromMetadata(meta))

	case todo.PurgeWorkflowStateMethod:
		return nil, o.purgeWorkflowState(ctx, meta)

	case todo.RecursivePurgeWorkflowStateMethod:
		return o.recursivePurgeWorkflowState(ctx, meta)

	case todo.ForkWorkflowHistory:
		return nil, backoff.Permanent(o.forkWorkflowHistory(ctx, request))

	case todo.RerunWorkflowInstance:
		return nil, backoff.Permanent(o.rerunWorkflowInstanceRequest(ctx, request))

	default:
		return nil, fmt.Errorf("no such method: %s", methodName)
	}
}

func (o *orchestrator) handleReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Workflow actor '%s': invoking reminder '%s'", o.actorID, reminder.Name)

	switch {
	// Must precede the new-event prefix arm: the janitor shares the prefix
	// deliberately (mixed-version routing) but has its own no-op semantics.
	case reminder.Name == janitorReminderName:
		return o.runJanitor(ctx, reminder)

	case strings.HasPrefix(reminder.Name, reminderPrefixStart),
		strings.HasPrefix(reminder.Name, reminderPrefixNewEvent),
		strings.HasPrefix(reminder.Name, reminderPrefixTimer),
		reminder.Name == reminderCascadeTerminate,
		reminder.Name == reminderNameParentNotify:
		return o.runWorkflowFromReminder(ctx, reminder)

	case strings.HasPrefix(reminder.Name, common.ReminderPrefixActivityResult):
		var ev backend.HistoryEvent
		if err := proto.Unmarshal(reminder.Data.GetValue(), &ev); err != nil {
			return fmt.Errorf("failed to unmarshal activity-result HistoryEvent: %w", err)
		}
		err := o.addWorkflowEvent(ctx, &ev, completionSender{})
		if errors.Is(err, api.ErrInstanceNotFound) {
			// The instance is gone (purged or never existed): ack so the scheduler
			// deletes this one-shot reminder. It is created with a retry-forever
			// failure policy, so an unclassified error here refires it every second
			// indefinitely; a batch of such orphans (activities completing across an
			// instance purge under placement churn) measurably degrades the whole
			// host.
			log.Warnf("Workflow actor '%s': dropping activity-result reminder '%s' for a purged instance", o.actorID, reminder.Name)
			return nil
		}
		return err

	default:
		return fmt.Errorf("unable to handle reminder '%s' for workflow actor '%s': unknown reminder type", reminder.Name, o.actorID)
	}
}

// runJanitor handles a fire of the per-instance janitor backstop reminder
// (WorkflowsFastPath). Semantics: self-delete against purged or
// terminal instances; cheap no-op (WITHOUT deactivating, so idle instances
// do not thrash the activation cache every period) when the inbox is empty;
// drive a normal turn when inbox rows are pending, which is the recovery
// event the janitor exists for.
func (o *orchestrator) runJanitor(ctx context.Context, reminder *actorapi.Reminder) error {
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if state == nil {
		o.deleteJanitor(ctx)
		return nil
	}

	if runtimestate.IsCompleted(o.rstate) {
		// Re-send the parent notification and re-assert retention before
		// self-deleting: the janitor owns recovery of either lost after a
		// terminal commit, including across a restart. The status is read
		// before the re-send, whose save may drop the cached runtime state.
		rstatus := runtimestate.RuntimeStatus(o.rstate)
		if state.ParentNotifyPending {
			if nerr := o.resendParentNotification(ctx, state, true); nerr != nil {
				return fmt.Errorf("failed to re-send the parent notification on janitor terminal path: %w", nerr)
			}
		}
		if rerr := o.handleRetention(ctx, rstatus); rerr != nil {
			return fmt.Errorf("failed to (re)create retention reminder on janitor terminal path: %w", rerr)
		}
		o.deleteJanitor(ctx)
		return nil
	}

	if len(state.Inbox) == 0 {
		// Mirror the empty-inbox stale-cache guard of runWorkflow: a peer
		// host may have written an inbox row since this cache was loaded
		// (a zombie writer racing an activation). Only a store read can see
		// it, but paying a full state load every period for every idle
		// instance is the dominant janitor cost, so the probe backs off
		// exponentially across consecutive no-op fires (1st, 2nd, 4th, 8th,
		// then every 8th). Recovery of that rare double-failure window
		// degrades from one period to at most eight; any real activity
		// resets the cadence.
		o.janitorIdleFires++
		n := o.janitorIdleFires
		if n <= 2 || n == 4 || n%8 == 0 {
			o.invalidateCachedState()
			state, _, err = o.loadInternalState(ctx)
			if err != nil {
				return err
			}
			if state == nil {
				o.deleteJanitor(ctx)
				return nil
			}
		}
	}

	if len(state.Inbox) == 0 {
		// No pending inbox rows, but the instance may have in-flight
		// activities whose only durable re-driver is this janitor (their
		// run-activity reminder is elided under
		// WorkflowsFastPath). Stalled workflows are excluded:
		// re-dispatching would replay the condition that stalled them.
		if o.rstate.GetStalled() == nil {
			if o.redispatchSuppressed() {
				// Recent life: in-flight activities are covered by their live
				// executions and the next period re-checks. Firing the re-dispatch
				// machinery against a merely-slow instance replays full-cost turns
				// through the scheduler (the measured collapse amplifier); a genuinely
				// idle-stalled instance goes stale within one period and re-dispatches
				// below.
				diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(ctx, diag.StatusJanitorRedispatchSuppressed)
				return nil
			}
			// Completions held for folding suppress the re-dispatch of their
			// tasks below, on the assumption their senders are alive and
			// re-driving. At a placement handoff that assumption breaks both
			// ways at once: the sender dies with its pod before re-delivering,
			// and the arming drive of the folding turn is lost (a wakeCtx
			// cancellation window, or a failed drive whose escalation was
			// suppressed). The completion is then captive in memory with no
			// driver at all, and this fire is the only thing that ever runs on
			// the instance. Drive a turn: runWorkflow folds pending
			// completions into its commit even with an empty inbox, restoring
			// at-most-one-period recovery for captive completions.
			if len(o.foldPending) > 0 {
				o.janitorIdleFires = 0
				diag.DefaultWorkflowMonitoring.WorkflowLocalWake(ctx, diag.StatusJanitorFoldRecovered)
				return o.runWorkflowFromReminder(ctx, reminder)
			}
			if unresolved := unresolvedScheduledTasks(state, o.foldEvents()); len(unresolved) > 0 {
				o.redispatchActivities(ctx, state, unresolved)
			}
		}
		return nil
	}

	// Pending inbox with no drive in sight: this fire IS the recovery.
	o.janitorIdleFires = 0
	diag.DefaultWorkflowMonitoring.WorkflowLocalWake(ctx, diag.StatusJanitorRecovered)
	return o.runWorkflowFromReminder(ctx, reminder)
}

func (o *orchestrator) runWorkflowFromReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	completed, err := o.runWorkflow(ctx, reminder)
	if o.rstate != nil && completed != todo.RunCompletedTrue && runtimestate.IsCompleted(o.rstate) {
		// The workflow completed on THIS turn (which reports RunCompletedFalse
		// because it consumed events). Release the cached state graph right here:
		// the history is the heavy part of a resident completed actor, and
		// dropping it in-turn frees the memory with no teardown machinery, no
		// channel, and no race with the drive-loop reclaim handshake.
		// The actor SHELL stays until swept by a follow-up empty-inbox ack or the
		// factory's idle reaper; post-completion client calls (status, purge)
		// reload the terminal state from the store.
		o.invalidateCachedState()
	}
	if completed == todo.RunCompletedTrue && (o.rstate == nil || runtimestate.IsCompleted(o.rstate)) {
		// Deactivate on empty-inbox acks only for terminal (or unknown-state)
		// workflows. A live workflow acking an empty-inbox reminder (routine after
		// batched turns) stays resident: its next event arrives shortly and the
		// cached state saves a full history reload Residency is bounded by the
		// engine's max concurrent workflow invocations, terminal turns releasing
		// their state above, and the factory idle reaper; placement churn and host
		// shutdown still halt resident actors.
		defer o.deactivate(o)
	}

	// We delete the reminder on success and on non-recoverable errors.
	// Returning nil signals that we want the execution to be retried in the next period interval
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("Workflow actor '%s': execution timed-out and will be retried later: '%v'", o.actorID, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("Workflow actor '%s': execution was canceled (process shutdown?) and will be retried later: '%v'", o.actorID, err)
		return err
	case wferrors.IsRecoverable(err):
		log.Warnf("Workflow actor '%s': execution failed with a recoverable error and will be retried later: '%v'", o.actorID, err)
		return err
	default: // Other error
		log.Errorf("Workflow actor '%s': execution failed with an error: %v", o.actorID, err)
		return err
	}
}
