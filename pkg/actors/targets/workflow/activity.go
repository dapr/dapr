/*
Copyright 2024 The Dapr Authors
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
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors/engine"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/internal"
	"github.com/dapr/dapr/pkg/components/wfbackend"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const activityStateKey = "activityState"

var (
	errExecutionAborted    = errors.New("execution aborted")
	ErrDuplicateInvocation = errors.New("duplicate invocation")
)

type runCompleted bool

const (
	runCompletedFalse runCompleted = false
	runCompletedTrue  runCompleted = true
)

type activity struct {
	appID             string
	actorID           string
	actorType         string
	workflowActorType string

	reminders reminders.Interface
	state     state.Interface
	engine    engine.Interface
	lock      *internal.Lock

	scheduler        wfbackend.ActivityScheduler
	aState           *activityState
	cachingDisabled  bool
	defaultTimeout   time.Duration
	reminderInterval time.Duration
	completed        atomic.Bool
}

// ActivityRequest represents a request by a worklow to invoke an activity.
type ActivityRequest struct {
	HistoryEvent []byte
}

type activityState struct {
	EventPayload []byte
}

type ActivityOptions struct {
	AppID             string
	ActivityActorType string
	WorkflowActorType string
	CachingDisabled   bool
	DefaultTimeout    *time.Duration
	ReminderInterval  *time.Duration

	State       state.Interface
	Reminders   reminders.Interface
	ActorEngine engine.Interface
}

func ActivityFactory(opts WorkflowOptions) targets.Factory {
	return func(actorID string) targets.Interface {
		reminderInterval := time.Minute * 1
		defaultTimeout := time.Hour * 1

		if opts.ReminderInterval != nil {
			reminderInterval = *opts.ReminderInterval
		}
		if opts.DefaultTimeout != nil {
			defaultTimeout = *opts.DefaultTimeout
		}

		return &activity{
			appID:             opts.AppID,
			actorID:           actorID,
			actorType:         opts.ActivityActorType,
			cachingDisabled:   opts.CachingDisabled,
			workflowActorType: opts.WorkflowActorType,
			reminderInterval:  reminderInterval,
			defaultTimeout:    defaultTimeout,
			state:             opts.State,
			reminders:         opts.Reminders,
			engine:            opts.ActorEngine,
			lock:              internal.NewLock(internal.LockOptions{ActorType: opts.ActivityActorType}),
		}
	}
}

// InvokeMethod implements actors.InternalActor and schedules the background execution of a workflow activity.
// Activities are scheduled by workflows and can execute for arbitrary lengths of time. Instead of executing
// activity logic directly, InvokeMethod creates a reminder that executes the activity logic. InvokeMethod
// returns immediately after creating the reminder, enabling the workflow to continue processing other events
// in parallel.
func (a *activity) InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	log.Debugf("Activity actor '%s': invoking method '%s'", a.actorID, req.GetMessage().GetMethod())

	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	cancel, err := a.lock.LockRequest(imReq)
	if err != nil {
		return nil, err
	}
	defer cancel()

	msg := imReq.Message()

	var ar ActivityRequest
	if err := gob.NewDecoder(bytes.NewReader(msg.GetData().GetValue())).Decode(&ar); err != nil {
		return nil, fmt.Errorf("failed to decode activity request: %w", err)
	}

	// Try to load activity state. If we find any, that means the activity invocation is a duplicate.
	if _, err := a.loadActivityState(ctx); err != nil {
		return nil, err
	}

	if msg.GetMethod() == "PurgeWorkflowState" {
		return nil, a.purgeActivityState(ctx)
	}

	// Save the request details to the state store in case we need it after recovering from a failure.
	if err = a.saveActivityState(ctx, &activityState{
		EventPayload: ar.HistoryEvent,
	}); err != nil {
		return nil, err
	}

	// The actual execution is triggered by a reminder
	return nil, a.createReliableReminder(ctx, nil)
}

// InvokeReminder implements actors.InternalActor and executes the activity logic.
// func (a *activity) InvokeReminder(ctx context.Context, reminder actors.Reminder, metadata map[string][]string) error {
func (a *activity) InvokeReminder(ctx context.Context, reminder *requestresponse.Reminder) error {
	cancel, err := a.lock.Lock()
	if err != nil {
		return err
	}
	defer cancel()

	log.Debugf("Activity actor '%s': invoking reminder '%s'", a.actorID, reminder.Name)

	state, _ := a.loadActivityState(ctx)
	// TODO: On error, reply with a failure - this requires support from durabletask-go to produce TaskFailure results
	if state == nil {
		return errors.New("no activity state found")
	}

	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, a.defaultTimeout)
	defer cancelTimeout()

	completed, err := a.executeActivity(timeoutCtx, reminder.Name, state.EventPayload)
	if completed == runCompletedTrue {
		a.completed.Store(true)
	}

	// Returning nil signals that we want the execution to be retried in the next period interval
	switch {
	case err == nil:
		// We delete the reminder on success and on non-recoverable errors.
		return actorerrors.ErrReminderCanceled
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("%s: execution of '%s' timed-out and will be retried later: %v", a.actorID, reminder.Name, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("%s: received cancellation signal while waiting for activity execution '%s'", a.actorID, reminder.Name)
		return nil
	case wfbackend.IsRecoverableError(err):
		log.Warnf("%s: execution failed with a recoverable error and will be retried later: %v", a.actorID, err)
		return nil
	default: // Other error
		log.Errorf("%s: execution failed with a non-recoverable error: %v", a.actorID, err)
		// TODO: Reply with a failure - this requires support from durabletask-go to produce TaskFailure results
		return actorerrors.ErrReminderCanceled
	}
}

func (a *activity) Completed() bool {
	return a.completed.Load()
}

func (a *activity) executeActivity(ctx context.Context, name string, eventPayload []byte) (runCompleted, error) {
	// TODO: @joshvanl: lock actor

	taskEvent, err := backend.UnmarshalHistoryEvent(eventPayload)
	if err != nil {
		return runCompletedTrue, err
	}
	activityName := ""
	if ts := taskEvent.GetTaskScheduled(); ts != nil {
		activityName = ts.GetName()
	} else {
		return runCompletedTrue, fmt.Errorf("invalid activity task event: '%s'", taskEvent.String())
	}

	endIndex := strings.Index(a.actorID, "::")
	if endIndex < 0 {
		return runCompletedTrue, fmt.Errorf("invalid activity actor ID: '%s'", a.actorID)
	}
	workflowID := a.actorID[0:endIndex]

	wi := &backend.ActivityWorkItem{
		SequenceNumber: int64(taskEvent.GetEventId()),
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
	wi.Properties[wfbackend.CallbackChannelProperty] = callback
	log.Debugf("Activity actor '%s': scheduling activity '%s' for workflow with instanceId '%s'", a.actorID, name, wi.InstanceID)
	err = a.scheduler(ctx, wi)
	if errors.Is(err, context.DeadlineExceeded) {
		return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("timed-out trying to schedule an activity execution - this can happen if too many activities are running in parallel or if the workflow engine isn't running: %w", err))
	} else if err != nil {
		return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("failed to schedule an activity execution: %w", err))
	}
	// Activity execution started
	start := time.Now()
	executionStatus := ""
	elapsed := float64(0)
	// Record metrics on exit
	defer func() {
		if executionStatus != "" {
			diag.DefaultWorkflowMonitoring.ActivityExecutionEvent(ctx, activityName, executionStatus, elapsed)
		}
	}()

loop:
	for {
		t := time.NewTimer(10 * time.Minute)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			// Activity execution failed with recoverable error
			elapsed = diag.ElapsedSince(start)
			executionStatus = diag.StatusRecoverable
			return runCompletedFalse, ctx.Err() // will be retried
		case <-t.C:
			if deadline, ok := ctx.Deadline(); ok {
				log.Warnf("Activity actor '%s': '%s' is still running - will keep waiting until '%v'", a.actorID, name, deadline)
			} else {
				log.Warnf("Activity actor '%s': '%s' is still running - will keep waiting indefinitely", a.actorID, name)
			}
		case completed := <-callback:
			if !t.Stop() {
				<-t.C
			}
			// Activity execution completed
			elapsed = diag.ElapsedSince(start)
			if completed {
				break loop
			} else {
				// Activity execution failed with recoverable error
				executionStatus = diag.StatusRecoverable
				return runCompletedFalse, wfbackend.NewRecoverableError(errExecutionAborted) // AbandonActivityWorkItem was called
			}
		}
	}
	log.Debugf("Activity actor '%s': activity completed for workflow with instanceId '%s' activityName '%s'", a.actorID, wi.InstanceID, name)

	// publish the result back to the workflow actor as a new event to be processed
	resultData, err := backend.MarshalHistoryEvent(wi.Result)
	if err != nil {
		// Returning non-recoverable error
		executionStatus = diag.StatusFailed
		return runCompletedTrue, err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(wfbackend.AddWorkflowEventMethod).
		WithActor(a.workflowActorType, workflowID).
		WithData(resultData).
		WithContentType(invokev1.OctetStreamContentType)

	_, err = a.engine.Call(ctx, req)
	switch {
	case err != nil:
		// Returning recoverable error, record metrics
		executionStatus = diag.StatusRecoverable
		return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("failed to invoke '%s' method on workflow actor: %w", wfbackend.AddWorkflowEventMethod, err))
	case wi.Result.GetTaskCompleted() != nil:
		// Activity execution completed successfully
		executionStatus = diag.StatusSuccess
	case wi.Result.GetTaskFailed() != nil:
		// Activity execution failed
		executionStatus = diag.StatusFailed
	}
	return runCompletedTrue, nil
}

// InvokeTimer implements actors.InternalActor
func (a *activity) InvokeTimer(ctx context.Context, reminder *requestresponse.Reminder) error {
	return errors.New("timers are not implemented")
}

// DeactivateActor implements actors.InternalActor
func (a *activity) DeactivateActor(ctx context.Context) error {
	log.Debugf("Activity actor '%s': deactivating", a.actorID)
	a.aState = nil // A bit of extra caution, shouldn't be necessary
	return nil
}

func (a *activity) loadActivityState(ctx context.Context) (*activityState, error) {
	// See if the state for this actor is already cached in memory.
	if a.aState != nil {
		return a.aState, nil
	}

	// Loading from the state store is only expected in process failure recovery scenarios.
	log.Debugf("Activity actor '%s': loading activity state", a.actorID)

	req := requestresponse.GetStateRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		Key:       activityStateKey,
	}
	res, err := a.state.Get(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("failed to load activity state: %w", err)
	}

	if len(res.Data) == 0 {
		// no data was found - this is expected on the initial invocation of the activity actor.
		return nil, nil
	}

	state := &activityState{}
	err = json.Unmarshal(res.Data, state)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal activity state: %w", err)
	}
	return state, nil
}

func (a *activity) saveActivityState(ctx context.Context, state *activityState) error {
	req := requestresponse.TransactionalRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		Operations: []requestresponse.TransactionalOperation{{
			Operation: requestresponse.Upsert,
			Request: requestresponse.TransactionalUpsert{
				Key:   activityStateKey,
				Value: state,
			},
		}},
	}
	if err := a.state.TransactionalStateOperation(ctx, &req); err != nil {
		return fmt.Errorf("failed to save activity state: %w", err)
	}

	if !a.cachingDisabled {
		a.aState = state
	}

	return nil
}

func (a *activity) purgeActivityState(ctx context.Context) error {
	log.Debugf("Activity actor '%s': purging activity state", a.actorID)
	err := a.state.TransactionalStateOperation(ctx, &requestresponse.TransactionalRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		Operations: []requestresponse.TransactionalOperation{{
			Operation: requestresponse.Delete,
			Request: requestresponse.TransactionalDelete{
				Key: activityStateKey,
			},
		}},
	})
	if err != nil {
		return fmt.Errorf("failed to delete activity state with error: %w", err)
	}

	return nil
}

func (a *activity) createReliableReminder(ctx context.Context, data any) error {
	const reminderName = "run-activity"
	log.Debugf("Activity actor '%s': creating reminder '%s' for immediate execution", a.actorID, reminderName)
	dataEnc, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to encode data as JSON: %w", err)
	}

	// TODO: @joshvanl: change to once shot when using Scheduler.
	return a.reminders.Create(ctx, &requestresponse.CreateReminderRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		Data:      dataEnc,
		DueTime:   "0s",
		Name:      reminderName,
		Period:    a.reminderInterval.String(),
	})
}

// DeactivateActor implements actors.InternalActor
func (a *activity) Deactivate(ctx context.Context) error {
	log.Debugf("Activity actor '%s': deactivating", a.actorID)
	a.aState = nil // A bit of extra caution, shouldn't be necessary
	a.lock.Close()
	return nil
}

// CloseUntil closes the actor but backs out sooner if the duration is reached.
func (a *activity) CloseUntil(d time.Duration) {
	a.lock.CloseUntil(d)
}
