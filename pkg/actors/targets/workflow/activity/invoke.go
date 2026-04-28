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

package activity

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Activities are scheduled by workflows and can execute for arbitrary lengths of time. Instead of executing
// activity logic directly, InvokeMethod creates a reminder that executes the activity logic. InvokeMethod
// returns immediately after creating the reminder, enabling the workflow to continue processing other events
// in parallel.
func (a *activity) handleInvoke(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	method := req.GetMessage().GetMethod()

	dueTime := time.Now()
	if s, ok := req.GetMetadata()[todo.MetadataActivityReminderDueTime]; ok && len(s.GetValues()) > 0 {
		unix, err := strconv.ParseInt(s.GetValues()[0], 10, 64)
		if err != nil {
			return nil, err
		}
		dueTime = time.UnixMilli(unix)
	}

	log.Debugf("Activity actor '%s': invoking method '%s'", a.actorID, method)

	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	msg := imReq.Message()

	invocation, activityName, err := decodeActivityInvocation(msg.GetData().GetValue())
	if err != nil {
		return nil, fmt.Errorf("failed to decode activity invocation: %w", err)
	}

	// The actual execution is triggered by a reminder
	return nil, a.createReminder(ctx, invocation, dueTime, activityName)
}

// decodeActivityInvocation parses an activity invocation payload. New
// orchestrators wrap the HistoryEvent in an ActivityInvocation envelope
// (which may carry PropagatedHistory) only when propagation is present.
// Otherwise, send a raw HistoryEvent for rolling-upgrade compatibility
// with older daprds. We try the envelope first, and fall back to a raw
// HistoryEvent if the envelope is absent or its HistoryEvent field is
// empty.
func decodeActivityInvocation(data []byte) (*protos.ActivityInvocation, *string, error) {
	var invocation protos.ActivityInvocation
	envelopeErr := proto.Unmarshal(data, &invocation)
	if envelopeErr == nil && invocation.GetHistoryEvent() != nil {
		return &invocation, taskScheduledName(invocation.GetHistoryEvent()), nil
	}

	// TODO: remove this legacy fallback in v1.19. Older daprds dispatch
	// activities as a raw HistoryEvent (no envelope); accept that shape so
	// rolling upgrades work, and drop it once the floor version is past
	// the rollout.
	var legacy backend.HistoryEvent
	if legacyErr := proto.Unmarshal(data, &legacy); legacyErr != nil {
		return nil, nil, fmt.Errorf("failed to decode activity invocation (envelope: %v; legacy: %w)", envelopeErr, legacyErr)
	}

	return &protos.ActivityInvocation{HistoryEvent: &legacy}, taskScheduledName(&legacy), nil
}

// taskScheduledName returns a pointer to the TaskScheduled event's name on
// the given history event
func taskScheduledName(e *backend.HistoryEvent) *string {
	if ts := e.GetTaskScheduled(); ts != nil {
		if n := ts.GetName(); n != "" {
			return &n
		}
	}
	return nil
}

func (a *activity) handleReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Activity actor '%s': invoking reminder '%s'", a.actorID, reminder.Name)

	// Try the new ActivityInvocation envelope format first. Fall back to
	// the legacy raw HistoryEvent payload for reminders created by
	// pre-propagation code.
	// TODO: remove this legacy fallback in v1.19 once reminders written by
	// pre-propagation daprds have been drained from the rollout.
	var invocation protos.ActivityInvocation
	if err := reminder.Data.UnmarshalTo(&invocation); err != nil {
		var legacy backend.HistoryEvent
		if legacyErr := reminder.Data.UnmarshalTo(&legacy); legacyErr != nil {
			return fmt.Errorf("failed to decode activity reminder (new format: %v; legacy: %w)", err, legacyErr)
		}
		invocation.HistoryEvent = &legacy
	}

	if invocation.GetHistoryEvent() == nil {
		return errors.New("activity reminder missing history event")
	}

	err := a.executeActivity(ctx, reminder.Name, &invocation)

	// Returning nil signals that we want the execution to be retried in the next
	// period interval
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("%s: execution of '%s' timed-out and will be retried later: %v", a.actorID, reminder.Name, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("%s: received cancellation signal while waiting for activity execution '%s'", a.actorID, reminder.Name)
		return err
	case wferrors.IsRecoverable(err):
		log.Warnf("%s: execution failed with a recoverable error and will be retried later: %v", a.actorID, err)
		return err
	default: // Other error
		log.Errorf("%s: execution failed with an error: %v", a.actorID, err)
		return err
	}
}
