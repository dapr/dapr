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

	"google.golang.org/protobuf/proto"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

// Activities are scheduled by workflows and can execute for arbitrary lengths of time. Instead of executing
// activity logic directly, InvokeMethod creates a reminder that executes the activity logic. InvokeMethod
// returns immediately after creating the reminder, enabling the workflow to continue processing other events
// in parallel.
func (a *activity) handleInvoke(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
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

	// The actual execution is triggered by a reminder
	return nil, a.createReminder(ctx, &his)
}

func (a *activity) handleReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Activity actor '%s': invoking reminder '%s'", a.actorID, reminder.Name)

	var state backend.HistoryEvent
	if err := reminder.Data.UnmarshalTo(&state); err != nil {
		return fmt.Errorf("failed to decode activity reminder: %w", err)
	}

	completed, err := a.executeActivity(ctx, reminder.Name, &state)
	if completed == todo.RunCompletedTrue {
		a.table.DeleteFromTableIn(a, 0)
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
