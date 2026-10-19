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
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Replay of held operations (the WorkflowsHeldOperations preview): a start or
// a new event that a daprd of this app held as a reminder while no daprd
// hosted this workflow actor type (backends/actors/held.go).
//
// Scheduler jobs fire in no particular order, so replaying each held
// operation on its own fire could apply a resume before the suspend it
// follows. Instead, whichever held reminder of an instance fires first
// replays all of them in the order they were requested, the start included:
// an event requested before a re-start belongs to the run it was aimed at.
// The first client call of an actor residency replays them too, so a call
// made directly once a host is up does not overtake them.
//
// An operation the instance refuses is dropped with a warning, as a caller
// with a connected worker would have been refused: an event for an instance
// that does not exist or is stalled, a start for an instance that is still
// running. A start whose instance has a termination pending waits for it
// instead. Each reminder is deleted once its operation is applied or
// dropped, so a later fire of it finds it gone and acks. Any other failure
// stops the replay and retries it under the reminder's failure policy: what
// was applied is gone, and the rest replays in order on the retry. If a
// delete fails, the operation's own fire applies it again: held operations
// are applied at least once, like every reminder.

// errHeldStartWaits stops a replay at a held start whose instance has a
// termination pending. The reminder retries until the termination is applied
// and the start can create the next run.
var errHeldStartWaits = errors.New("held start waits for the pending termination of the instance")

// heldOp is one held operation of this instance, decoded from its reminder.
type heldOp struct {
	name string

	// One of start (with its encoded request in raw) or event is set.
	start *backend.CreateWorkflowInstanceRequest
	raw   []byte
	event *backend.HistoryEvent
}

// requested is when the operation was requested, which orders the events.
func (h heldOp) requested() time.Time {
	if h.start != nil {
		return h.start.GetStartEvent().GetTimestamp().AsTime()
	}
	return h.event.GetTimestamp().AsTime()
}

// replayHeld replays every held operation of this instance on the fire of
// one of their reminders.
func (o *orchestrator) replayHeld(ctx context.Context, fired *actorapi.Reminder) error {
	if !o.heldByThisApp(fired) {
		return nil
	}

	ops, err := o.listHeld(ctx)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(ops, func(op heldOp) bool { return op.name == fired.Name }) {
		// An earlier replay applied it.
		return nil
	}

	return o.replayHeldOps(ctx, ops)
}

// replayHeldBeforeCall replays the held operations of this instance before
// the first client call of this actor residency. Held operations are only
// created while no daprd hosts this actor type, so once they are replayed
// here, later calls of the residency cannot overtake any.
func (o *orchestrator) replayHeldBeforeCall(ctx context.Context) error {
	if !o.heldOperations || o.heldReplayed.Load() {
		return nil
	}

	ops, err := o.listHeld(ctx)
	if err != nil {
		return err
	}

	err = o.replayHeldOps(ctx, ops)
	if errors.Is(err, errHeldStartWaits) {
		// The start's reminder retries it, and the next client call checks
		// again. Failing this call would not get the start applied sooner.
		return nil
	}
	if err != nil {
		return err
	}

	o.heldReplayed.Store(true)
	return nil
}

func (o *orchestrator) replayHeldOps(ctx context.Context, ops []heldOp) error {
	for _, op := range ops {
		var err error
		if op.start != nil {
			err = o.replayHeldStart(ctx, op)
		} else {
			err = o.replayHeldEvent(ctx, op)
		}
		if err != nil {
			return err
		}
		o.deleteHeld(ctx, op.name, "applied held operation")
	}
	return nil
}

// deleteRemindersKeepingHeld deletes the reminders of a completed run one by
// one when held operations of the instance are still to be replayed. Deleting
// them by actor ID would drop those too, such as a held start waiting for
// this run's termination. It reports false when none are held, for the
// caller to delete by actor ID.
func (o *orchestrator) deleteRemindersKeepingHeld(ctx context.Context) (bool, error) {
	if !o.heldOperations {
		return false, nil
	}

	actorType := o.actorTypeBuilder.Workflow(o.appID)
	reminders, err := o.reminders.List(ctx, &actorapi.ListRemindersRequest{
		ActorType: actorType,
		ActorID:   &o.actorID,
	})
	if err != nil {
		return false, fmt.Errorf("failed to list reminders: %w", err)
	}
	if !slices.ContainsFunc(reminders, isHeldReminder) {
		return false, nil
	}

	for _, r := range reminders {
		if r == nil || isHeldReminder(r) {
			continue
		}
		err := o.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
			Name:      r.Name,
			ActorType: actorType,
			ActorID:   o.actorID,
		})
		if err != nil && status.Code(err) != codes.NotFound {
			return true, fmt.Errorf("failed to delete reminder '%s': %w", r.Name, err)
		}
	}
	return true, nil
}

func isHeldReminder(r *actorapi.Reminder) bool {
	return r != nil && (r.Name == common.ReminderNameHeldStart || strings.HasPrefix(r.Name, common.ReminderPrefixHeldEvent))
}

// isHeldCall reports whether an invoke of method may have been held while no
// daprd hosted this actor type: a client's start, raise event, terminate,
// suspend or resume. Child workflow creates, activity results and child
// completions are never held.
func isHeldCall(method string, request []byte, e *backend.HistoryEvent) bool {
	switch method {
	case todo.CreateWorkflowInstanceMethod:
		var req backend.CreateWorkflowInstanceRequest
		if err := proto.Unmarshal(request, &req); err != nil {
			return false
		}
		return req.GetStartEvent().GetExecutionStarted().GetParentInstance() == nil
	case todo.AddWorkflowEventMethod:
		return e.GetEventRaised() != nil ||
			e.GetExecutionTerminated() != nil ||
			e.GetExecutionSuspended() != nil ||
			e.GetExecutionResumed() != nil
	default:
		return false
	}
}

// listHeld returns the held operations of this instance in replay order.
// One that cannot be decoded is deleted and dropped.
func (o *orchestrator) listHeld(ctx context.Context) ([]heldOp, error) {
	reminders, err := o.reminders.List(ctx, &actorapi.ListRemindersRequest{
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   &o.actorID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list held operations: %w", err)
	}

	var ops []heldOp
	for _, r := range reminders {
		switch {
		case r == nil:
		case r.Name == common.ReminderNameHeldStart:
			var req backend.CreateWorkflowInstanceRequest
			if err := r.Data.UnmarshalTo(&req); err != nil {
				log.Warnf("Workflow actor '%s': dropping held start that cannot be decoded: %v", o.actorID, err)
				o.deleteHeld(ctx, r.Name, "undecodable held start")
				continue
			}
			if req.GetStartEvent().GetExecutionStarted() == nil {
				log.Warnf("Workflow actor '%s': dropping held start without an execution started event", o.actorID)
				o.deleteHeld(ctx, r.Name, "held start without a start event")
				continue
			}
			ops = append(ops, heldOp{name: r.Name, start: &req, raw: r.Data.GetValue()})

		case strings.HasPrefix(r.Name, common.ReminderPrefixHeldEvent):
			var e backend.HistoryEvent
			if err := r.Data.UnmarshalTo(&e); err != nil {
				log.Warnf("Workflow actor '%s': dropping held event that cannot be decoded: %v", o.actorID, err)
				o.deleteHeld(ctx, r.Name, "undecodable held event")
				continue
			}
			ops = append(ops, heldOp{name: r.Name, event: &e})
		}
	}

	slices.SortFunc(ops, func(a, b heldOp) int {
		if c := a.requested().Compare(b.requested()); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})

	return ops, nil
}

func (o *orchestrator) deleteHeld(ctx context.Context, name, what string) {
	o.deleteReminderTolerant(ctx, &actorapi.DeleteReminderRequest{
		Name:      name,
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   o.actorID,
	}, what)
}

// replayHeldStart replays a held workflow start.
func (o *orchestrator) replayHeldStart(ctx context.Context, op heldOp) error {
	startEvent := op.start.GetStartEvent()
	if common.HeldOperationExpired(startEvent.GetTimestamp().AsTime()) {
		return o.failExpiredHeldStart(ctx, startEvent)
	}

	err := o.createWorkflowInstance(ctx, op.raw)
	if status.Code(err) != codes.AlreadyExists {
		return err
	}

	state, _, lerr := o.loadInternalState(ctx)
	if lerr != nil {
		return lerr
	}
	if terminationPending(state) {
		// A terminate requested before this start is in the inbox but not
		// applied yet: the start creates the next run once it is.
		return fmt.Errorf("%w: %w", errHeldStartWaits, err)
	}

	// The caller was told the start was accepted, so this is the only place
	// the refusal can be reported.
	log.Warnf("Workflow actor '%s': dropping held start: %v", o.actorID, err)
	return nil
}

func terminationPending(state *wfenginestate.State) bool {
	return state != nil && slices.ContainsFunc(state.Inbox, func(e *backend.HistoryEvent) bool {
		return e.GetExecutionTerminated() != nil
	})
}

// replayHeldEvent replays a held raise event, terminate, suspend or resume.
func (o *orchestrator) replayHeldEvent(ctx context.Context, op heldOp) error {
	e := op.event
	if common.HeldOperationExpired(e.GetTimestamp().AsTime()) {
		log.Warnf("Workflow actor '%s': dropping %T event held for longer than %s while no workflow worker was connected", o.actorID, e.GetEventType(), common.HeldOperationTimeout())
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.AddEvent, diag.StatusHeldExpired, 0)
		return nil
	}

	err := o.addWorkflowEvent(ctx, e, completionSender{})
	if errors.Is(err, api.ErrInstanceNotFound) || errors.Is(err, api.ErrStalled) {
		// Refused for good, as a caller with a connected worker would have
		// been. Retrying would only hold back the events behind it. A held
		// start of this instance requested earlier was replayed before it.
		log.Warnf("Workflow actor '%s': dropping held %T event: %v", o.actorID, e.GetEventType(), err)
		return nil
	}
	return err
}

// heldByThisApp reports whether a held operation was held by this app. The
// scheduler only lets this app create these reminders on its workflow actor
// type, so anything else is dropped.
func (o *orchestrator) heldByThisApp(reminder *actorapi.Reminder) bool {
	if reminder.SourceAppID == "" || reminder.SourceAppID == o.appID {
		return true
	}
	log.Warnf("Workflow actor '%s': dropping held operation reminder '%s' from app '%s'", o.actorID, reminder.Name, reminder.SourceAppID)
	return false
}

// failExpiredHeldStart records a start held past the hold timeout as a
// FAILED instance, so a client polling the instance it was told had started
// learns what happened to it.
func (o *orchestrator) failExpiredHeldStart(ctx context.Context, startEvent *backend.HistoryEvent) error {
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state != nil {
		if completedAt, ok := heldStartFailedAt(state, startEvent); ok {
			// An earlier fire saved this failure but did not arm its
			// retention: arm it now rather than leave the instance unpurged.
			return o.handleRetention(ctx, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, completedAt)
		}
		// The instance ID was taken while this start was held: there is no
		// instance to record the failure on.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.CreateWorkflow, diag.StatusHeldExpired, 0)
		log.Warnf("Workflow actor '%s': dropping start held for longer than %s while no workflow worker was connected", o.actorID, common.HeldOperationTimeout())
		return nil
	}

	msg := fmt.Sprintf("the workflow start was held for longer than %s while no workflow worker was connected", common.HeldOperationTimeout())
	log.Warnf("Workflow actor '%s': %s; failing the workflow instance", o.actorID, msg)

	now := time.Now()
	state = wfenginestate.NewState(o.stateOptions())
	state.AddToHistory(startEvent)
	state.AddToHistory(&backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(now),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    staterrors.ErrorTypeHeldStartExpired,
					ErrorMessage: msg,
				},
			},
		},
	})
	if err := o.signAndSaveState(ctx, state); err != nil {
		return err
	}
	// Recorded once the failure is saved, so a retried fire is not counted
	// again.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.CreateWorkflow, diag.StatusHeldExpired, 0)
	// The views were never primed for this state: reload on the next call.
	o.invalidateCachedState()

	return o.handleRetention(ctx, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, now)
}

// heldStartFailedAt reports whether state is the failure failExpiredHeldStart
// recorded for startEvent, and when it was recorded.
func heldStartFailedAt(state *wfenginestate.State, startEvent *backend.HistoryEvent) (time.Time, bool) {
	if len(state.History) != 2 {
		return time.Time{}, false
	}
	started, completed := state.History[0], state.History[1]
	if started.GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue() !=
		startEvent.GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue() ||
		completed.GetExecutionCompleted().GetFailureDetails().GetErrorType() != staterrors.ErrorTypeHeldStartExpired {
		return time.Time{}, false
	}
	return completed.GetTimestamp().AsTime(), true
}
