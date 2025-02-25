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
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/actors"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/engine"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
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

	table     table.Interface
	engine    engine.Interface
	state     state.Interface
	reminders reminders.Interface

	scheduler          todo.ActivityScheduler
	reminderInterval   time.Duration
	schedulerReminders bool
}

type ActivityOptions struct {
	AppID              string
	ActivityActorType  string
	WorkflowActorType  string
	ReminderInterval   *time.Duration
	Scheduler          todo.ActivityScheduler
	Actors             actors.Interface
	SchedulerReminders bool
}

func ActivityFactory(ctx context.Context, opts ActivityOptions) (targets.Factory, error) {
	table, err := opts.Actors.Table(ctx)
	if err != nil {
		return nil, err
	}

	engine, err := opts.Actors.Engine(ctx)
	if err != nil {
		return nil, err
	}

	state, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}

	reminders, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}

	return func(actorID string) targets.Interface {
		reminderInterval := time.Minute * 1

		if opts.ReminderInterval != nil {
			reminderInterval = *opts.ReminderInterval
		}

		return &activity{
			appID:              opts.AppID,
			actorID:            actorID,
			actorType:          opts.ActivityActorType,
			workflowActorType:  opts.WorkflowActorType,
			reminderInterval:   reminderInterval,
			table:              table,
			engine:             engine,
			state:              state,
			reminders:          reminders,
			scheduler:          opts.Scheduler,
			schedulerReminders: opts.SchedulerReminders,
		}
	}, nil
}

// InvokeMethod implements actors.InternalActor and schedules the background execution of a workflow activity.
// Activities are scheduled by workflows and can execute for arbitrary lengths of time. Instead of executing
// activity logic directly, InvokeMethod creates a reminder that executes the activity logic. InvokeMethod
// returns immediately after creating the reminder, enabling the workflow to continue processing other events
// in parallel.
func (a *activity) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	log.Debugf("Activity actor '%s': invoking method '%s'", a.actorID, req.GetMessage().GetMethod())

	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	msg := imReq.Message()

	var his backend.HistoryEvent
	if err = proto.Unmarshal(msg.GetData().GetValue(), &his); err != nil {
		return nil, fmt.Errorf("failed to decode activity request: %w", err)
	}

	if msg.GetMethod() == "PurgeWorkflowState" {
		return nil, a.purgeActivityState(ctx)
	}

	// The actual execution is triggered by a reminder
	return nil, a.createReliableReminder(ctx, &his)
}

// InvokeReminder implements actors.InternalActor and executes the activity logic.
func (a *activity) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Activity actor '%s': invoking reminder '%s'", a.actorID, reminder.Name)

	var state backend.HistoryEvent
	if err := reminder.Data.UnmarshalTo(&state); err != nil {
		return fmt.Errorf("failed to decode activity reminder: %w", err)
	}

	completed, err := a.executeActivity(ctx, reminder.Name, &state)
	if completed == runCompletedTrue {
		a.table.DeleteFromTable(a.actorType, a.actorID)
	}

	// Returning nil signals that we want the execution to be retried in the next period interval
	switch {
	case err == nil:
		if a.schedulerReminders {
			return nil
		}
		// We delete the reminder on success and on non-recoverable errors.
		return actorerrors.ErrReminderCanceled
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("%s: execution of '%s' timed-out and will be retried later: %v", a.actorID, reminder.Name, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("%s: received cancellation signal while waiting for activity execution '%s'", a.actorID, reminder.Name)
		if a.schedulerReminders {
			return err
		}
		return nil
	case wferrors.IsRecoverable(err):
		log.Warnf("%s: execution failed with a recoverable error and will be retried later: %v", a.actorID, err)
		if a.schedulerReminders {
			return err
		}
		return nil
	default: // Other error
		log.Errorf("%s: execution failed with an error: %v", a.actorID, err)
		if a.schedulerReminders {
			return err
		}
		// TODO: Reply with a failure - this requires support from durabletask-go to produce TaskFailure results
		return actorerrors.ErrReminderCanceled
	}
}

func (a *activity) executeActivity(ctx context.Context, name string, taskEvent *backend.HistoryEvent) (runCompleted, error) {
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
		Properties:     make(map[string]any),
	}

	// Executing activity code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	// TODO: Need to come up with a design for timeouts. Some activities may need to run for hours but we also need
	//       to handle the case where the app crashes and never responds to the workflow. It may be necessary to
	//       introduce some kind of heartbeat protocol to help identify such cases.
	callback := make(chan bool, 1)
	wi.Properties[todo.CallbackChannelProperty] = callback
	log.Debugf("Activity actor '%s': scheduling activity '%s' for workflow with instanceId '%s'", a.actorID, name, wi.InstanceID)
	err := a.scheduler(ctx, wi)
	if errors.Is(err, context.DeadlineExceeded) {
		return runCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("timed-out trying to schedule an activity execution - this can happen if too many activities are running in parallel or if the workflow engine isn't running: %w", err))
	} else if err != nil {
		return runCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to schedule an activity execution: %w", err))
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

	select {
	case <-ctx.Done():
		// Activity execution failed with recoverable error
		elapsed = diag.ElapsedSince(start)
		executionStatus = diag.StatusRecoverable
		return runCompletedFalse, ctx.Err() // will be retried
	case completed := <-callback:
		elapsed = diag.ElapsedSince(start)
		if !completed {
			// Activity execution failed with recoverable error
			executionStatus = diag.StatusRecoverable
			return runCompletedFalse, wferrors.NewRecoverable(errExecutionAborted) // AbandonActivityWorkItem was called
		}
	}
	log.Debugf("Activity actor '%s': activity completed for workflow with instanceId '%s' activityName '%s'", a.actorID, wi.InstanceID, name)

	// publish the result back to the workflow actor as a new event to be processed
	resultData, err := proto.Marshal(wi.Result)
	if err != nil {
		// Returning non-recoverable error
		executionStatus = diag.StatusFailed
		return runCompletedTrue, err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.AddWorkflowEventMethod).
		WithActor(a.workflowActorType, workflowID).
		WithData(resultData).
		WithContentType(invokev1.ProtobufContentType)

	_, err = a.engine.Call(ctx, req)
	switch {
	case err != nil:
		// Returning recoverable error, record metrics
		executionStatus = diag.StatusRecoverable
		return runCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to invoke '%s' method on workflow actor: %w", todo.AddWorkflowEventMethod, err))
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
func (a *activity) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (a *activity) purgeActivityState(ctx context.Context) error {
	log.Debugf("Activity actor '%s': purging activity state", a.actorID)
	err := a.state.TransactionalStateOperation(ctx, true, &actorapi.TransactionalRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		Operations: []actorapi.TransactionalOperation{{
			Operation: actorapi.Delete,
			Request: actorapi.TransactionalDelete{
				Key: activityStateKey,
			},
		}},
	})
	if err != nil {
		return fmt.Errorf("failed to delete activity state with error: %w", err)
	}

	return nil
}

func (a *activity) createReliableReminder(ctx context.Context, his *backend.HistoryEvent) error {
	const reminderName = "run-activity"
	log.Debugf("Activity actor '%s||%s': creating reminder '%s' for immediate execution", a.actorType, a.actorID, reminderName)

	var period string
	var oneshot bool
	if a.schedulerReminders {
		oneshot = true
	} else {
		period = a.reminderInterval.String()
	}

	anydata, err := anypb.New(his)
	if err != nil {
		return err
	}

	return a.reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		DueTime:   "0s",
		Name:      reminderName,
		Period:    period,
		IsOneShot: oneshot,
		Data:      anydata,
	})
}

// DeactivateActor implements actors.InternalActor
func (a *activity) Deactivate() error {
	log.Debugf("Activity actor '%s': deactivated", a.actorID)
	return nil
}

func (a *activity) InvokeStream(context.Context, *internalsv1pb.InternalInvokeRequest, chan<- *internalsv1pb.InternalInvokeResponse) error {
	return errors.New("not implemented")
}

// Key returns the key for this unique actor.
func (a *activity) Key() string {
	return a.actorType + actorapi.DaprSeparator + a.actorID
}

// Type returns the type of actor.
func (a *activity) Type() string {
	return a.actorType
}

// ID returns the ID of the actor.
func (a *activity) ID() string {
	return a.actorID
}
