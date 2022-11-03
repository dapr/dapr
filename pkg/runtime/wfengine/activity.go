/*
Copyright 2022 The Dapr Authors
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
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

type activityActor struct {
	actorRuntime actors.Actors
	scheduler    workflowScheduler
}

// NewActivityActor creates an [actors.InternalActor] for executing workflow activity logic.
func NewActivityActor(scheduler workflowScheduler) actors.InternalActor {
	return &activityActor{
		scheduler: scheduler,
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
	// This actor doesn't define any methods, and uses the method name and payload as the reminder name and payload.
	err := a.createReliableReminder(ctx, actorID, methodName, data)
	return nil, err
}

// InvokeReminder implements actors.InternalActor and executes the activity logic.
// TODO: Need to review how errors for reminders are handled.
func (a *activityActor) InvokeReminder(ctx context.Context, actorID string, reminderName string, data any, dueTime string, period string) error {
	taskEvent, err := backend.UnmarshalHistoryEvent(data.([]byte))
	if err != nil {
		return err
	}

	endIndex := strings.LastIndex(actorID, "#")
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
		return fmt.Errorf("failed to schedule an activity execution: %w", err)
	}

loop:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Minute):
			wfLogger.Warnf("%s: still waiting for '%s' to complete...", actorID, reminderName)
		case <-callback:
			break loop
		}
	}

	// publish the result back to the workflow actor as a new event to be processed
	resultData, err := backend.MarshalHistoryEvent(wi.Result)
	if err != nil {
		return err
	}
	req := invokev1.
		NewInvokeMethodRequest(AddWorkflowEventMethod).
		WithActor(WorkflowActorType, workflowID).
		WithRawData(resultData, invokev1.OctetStreamContentType)
	if _, err := a.actorRuntime.Call(ctx, req); err != nil {
		return err
	}
	return nil
}

// InvokeTimer implements actors.InternalActor
func (*activityActor) InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error {
	return nil // timers are not used
}

// DeactivateActor implements actors.InternalActor
func (*activityActor) DeactivateActor(ctx context.Context, actorID string) error {
	return nil // nothing to do - this actor maintains no state
}

func (a *activityActor) createReliableReminder(ctx context.Context, actorID string, name string, data any) error {
	wfLogger.Debugf("%s: creating '%s' reminder for immediate execution", actorID, name)
	return a.actorRuntime.CreateReminder(ctx, &actors.CreateReminderRequest{
		ActorType: ActivityActorType,
		ActorID:   actorID,
		Data:      data,
		DueTime:   "0s",
		Name:      name,
		Period:    "", // TODO: Add configurable period to enable durability
	})
}
