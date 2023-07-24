/*
Copyright 2023 The Dapr Authors
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
package wfengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

var ErrDuplicateInvocation = errors.New("duplicate invocation")

const activityStateKey = "activityState"

type activityActor struct {
	actorRuntime     actors.Actors
	scheduler        workflowScheduler
	statesCache      sync.Map
	cachingDisabled  bool
	defaultTimeout   time.Duration
	reminderInterval time.Duration
	config           wfConfig
}

// ActivityRequest represents a request by a worklow to invoke an activity.
type ActivityRequest struct {
	HistoryEvent []byte
}

type activityState struct {
	EventPayload []byte
}

// NewActivityActor creates an internal activity actor for executing workflow activity logic.
func NewActivityActor(scheduler workflowScheduler, config wfConfig) *activityActor {
	return &activityActor{
		scheduler:        scheduler,
		defaultTimeout:   1 * time.Hour,
		reminderInterval: 1 * time.Minute,
		config:           config,
	}
}

// SetActorRuntime implements actors.InternalActor
func (a *activityActor) SetActorRuntime(actorsRuntime actors.Actors) {
	a.actorRuntime = actorsRuntime
}

// InvokeMethod implements actors.InternalActor and schedules the background execution of a workflow activity.
// Activities are scheduled by workflows and can execute for arbitrary lengths of time. Instead of executing
// activity logic directly, InvokeMethod creates a reminder that executes the activity logic. InvokeMethod
// returns immediately after creating the reminder, enabling the workflow to continue processing other events
// in parallel.
func (a *activityActor) InvokeMethod(ctx context.Context, actorID string, methodName string, data []byte) (any, error) {
	var ar ActivityRequest
	if err := actors.DecodeInternalActorData(data, &ar); err != nil {
		return nil, fmt.Errorf("failed to decode activity request: %w", err)
	}

	// Try to load activity state. If we find any, that means the activity invocation is a duplicate.
	if _, err := a.loadActivityState(ctx, actorID); err != nil {
		return nil, err
	}

	if methodName == "PurgeWorkflowState" {
		return nil, a.purgeActivityState(ctx, actorID)
	}

	// Save the request details to the state store in case we need it after recovering from a failure.
	state := activityState{
		EventPayload: ar.HistoryEvent,
	}

	if err := a.saveActivityState(ctx, actorID, state); err != nil {
		return nil, err
	}

	// The actual execution is triggered by a reminder
	err := a.createReliableReminder(ctx, actorID, nil)
	return nil, err
}

// InvokeReminder implements actors.InternalActor and executes the activity logic.
func (a *activityActor) InvokeReminder(ctx context.Context, actorID string, reminderName string, data []byte, dueTime string, period string) error {
	wfLogger.Debugf("invoking reminder '%s' on activity actor '%s'", reminderName, actorID)

	var generation uint64
	if err := actors.DecodeInternalActorReminderData(data, &generation); err != nil {
		// Likely the result of an incompatible activity reminder format change. This is non-recoverable.
		return err
	}
	state, _ := a.loadActivityState(ctx, actorID)
	// TODO: On error, reply with a failure - this requires support from durabletask-go to produce TaskFailure results

	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, a.defaultTimeout)
	defer cancelTimeout()

	if err := a.executeActivity(timeoutCtx, actorID, reminderName, state.EventPayload); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			wfLogger.Warnf("%s: execution of '%s' timed-out and will be retried later: %v", actorID, reminderName, err)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else if _, ok := err.(recoverableError); ok {
			wfLogger.Warnf("%s: execution failed with a recoverable error and will be retried later: %v", actorID, err)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else {
			wfLogger.Errorf("%s: execution failed with a non-recoverable error: %v", actorID, err)
			// TODO: Reply with a failure - this requires support from durabletask-go to produce TaskFailure results
		}
	}

	// TODO: Purge actor state based on some data retention policy

	// We delete the reminder on success and on non-recoverable errors.
	return actors.ErrReminderCanceled
}

func (a *activityActor) executeActivity(ctx context.Context, actorID string, name string, eventPayload []byte) error {
	taskEvent, err := backend.UnmarshalHistoryEvent(eventPayload)
	if err != nil {
		return err
	}

	endIndex := strings.Index(actorID, "::")
	if endIndex < 0 {
		return fmt.Errorf("invalid activity actor ID: %s", actorID)
	}
	workflowID := actorID[0:endIndex]

	wi := &backend.ActivityWorkItem{
		SequenceNumber: int64(taskEvent.EventId),
		InstanceID:     api.InstanceID(workflowID),
		NewEvent:       taskEvent,
		Properties:     make(map[string]interface{}),
	}

	// Executing activity code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	// TODO: Need to come up with a design for timeouts. Some activities may need to run for hours but we also need
	//       to handle the case where the app crashes and never responds to the workflow. It may be necessary to
	//       introduce some kind of heartbeat protocol to help identify such cases.
	callback := make(chan bool)
	wi.Properties[CallbackChannelProperty] = callback
	if err = a.scheduler.ScheduleActivity(ctx, wi); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return newRecoverableError(fmt.Errorf("timed-out trying to schedule an activity execution - this can happen if too many activities are running in parallel or if the workflow engine isn't running: %w", err))
		}
		return newRecoverableError(fmt.Errorf("failed to schedule an activity execution: %w", err))
	}

loop:
	for {
		t := time.NewTimer(10 * time.Minute)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return ctx.Err()
		case <-t.C:
			if deadline, ok := ctx.Deadline(); ok {
				wfLogger.Warnf("%s: '%s' is still running - will keep waiting until %v", actorID, name, deadline)
			} else {
				wfLogger.Warnf("%s: '%s' is still running - will keep waiting indefinitely", actorID, name)
			}
		case completed := <-callback:
			if !t.Stop() {
				<-t.C
			}
			if completed {
				break loop
			} else {
				return newRecoverableError(errExecutionAborted)
			}
		}
	}

	// publish the result back to the workflow actor as a new event to be processed
	resultData, err := backend.MarshalHistoryEvent(wi.Result)
	if err != nil {
		return err
	}
	req := invokev1.
		NewInvokeMethodRequest(AddWorkflowEventMethod).
		WithActor(a.config.workflowActorType, workflowID).
		WithRawDataBytes(resultData).
		WithContentType(invokev1.OctetStreamContentType)
	defer req.Close()

	resp, err := a.actorRuntime.Call(ctx, req)
	if err != nil {
		return newRecoverableError(fmt.Errorf("failed to invoke '%s' method on workflow actor: %w", AddWorkflowEventMethod, err))
	}
	defer resp.Close()
	return nil
}

// InvokeTimer implements actors.InternalActor
func (*activityActor) InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error {
	return errors.New("timers are not implemented")
}

// DeactivateActor implements actors.InternalActor
func (a *activityActor) DeactivateActor(ctx context.Context, actorID string) error {
	wfLogger.Debugf("deactivating activity actor '%s'", actorID)
	a.statesCache.Delete(actorID)
	return nil
}

func (a *activityActor) loadActivityState(ctx context.Context, actorID string) (activityState, error) {
	// See if the state for this actor is already cached in memory.
	result, ok := a.statesCache.Load(actorID)
	if ok {
		cachedState := result.(activityState)
		return cachedState, nil
	}

	// Loading from the state store is only expected in process failure recovery scenarios.
	wfLogger.Debugf("%s: loading activity state", actorID)

	req := actors.GetStateRequest{
		ActorType: a.config.activityActorType,
		ActorID:   actorID,
		Key:       activityStateKey,
	}
	res, err := a.actorRuntime.GetState(ctx, &req)
	if err != nil {
		return activityState{}, fmt.Errorf("failed to load activity state: %w", err)
	}

	if len(res.Data) == 0 {
		// no data was found - this is expected on the initial invocation of the activity actor.
		return activityState{}, nil
	}

	var state activityState
	if err = json.Unmarshal(res.Data, &state); err != nil {
		return activityState{}, fmt.Errorf("failed to unmarshal activity state: %w", err)
	}
	return state, nil
}

func (a *activityActor) saveActivityState(ctx context.Context, actorID string, state activityState) error {
	req := actors.TransactionalRequest{
		ActorType: a.config.activityActorType,
		ActorID:   actorID,
		Operations: []actors.TransactionalOperation{{
			Operation: actors.Upsert,
			Request: actors.TransactionalUpsert{
				Key:   activityStateKey,
				Value: state,
			},
		}},
	}
	if err := a.actorRuntime.TransactionalStateOperation(ctx, &req); err != nil {
		return fmt.Errorf("failed to save activity state: %w", err)
	}

	if !a.cachingDisabled {
		a.statesCache.Store(actorID, state)
	}
	return nil
}

func (a *activityActor) purgeActivityState(ctx context.Context, actorID string) error {
	req := actors.TransactionalRequest{
		ActorType: a.config.activityActorType,
		ActorID:   actorID,
		Operations: []actors.TransactionalOperation{{
			Operation: actors.Delete,
			Request: actors.TransactionalDelete{
				Key: activityStateKey,
			},
		}},
	}
	if err := a.actorRuntime.TransactionalStateOperation(ctx, &req); err != nil {
		return fmt.Errorf("failed to delete activity state with error: %w", err)
	}

	return nil
}

func (a *activityActor) createReliableReminder(ctx context.Context, actorID string, data any) error {
	const reminderName = "run-activity"
	wfLogger.Debugf("%s: creating '%s' reminder for immediate execution", actorID, reminderName)
	dataEnc, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to encode data as JSON: %w", err)
	}
	return a.actorRuntime.CreateReminder(ctx, &actors.CreateReminderRequest{
		ActorType: a.config.activityActorType,
		ActorID:   actorID,
		Data:      dataEnc,
		DueTime:   "0s",
		Name:      reminderName,
		Period:    a.reminderInterval.String(),
	})
}
