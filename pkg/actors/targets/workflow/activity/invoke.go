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
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
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

	var his backend.HistoryEvent
	if err = proto.Unmarshal(msg.GetData().GetValue(), &his); err != nil {
		return nil, fmt.Errorf("failed to decode activity request: %w", err)
	}

	var activityName *string
	if ts := his.GetTaskScheduled(); ts != nil {
		if n := ts.GetName(); n != "" {
			activityName = &n
		}
	}

	// The actual execution is triggered by a reminder
	return nil, a.createReminder(ctx, &his, dueTime, activityName)
}

func (a *activity) handleReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Activity actor '%s': invoking reminder '%s'", a.actorID, reminder.Name)

	state, err := a.decodeReminderEvent(reminder)
	if err != nil {
		return err
	}

	err = a.executeActivity(ctx, reminder.Name, state)
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
	default:
		log.Errorf("%s: execution failed with an error: %v", a.actorID, err)
		return err
	}
}

// decodeReminderEvent returns the scheduling HistoryEvent carried by a
// reminder. Normal same-namespace invocations pack the HistoryEvent
// directly; cross-namespace xns-exec reminders instead wrap it in a
// CrossNSDispatchRequest (caller-supplied deterministic key in the
// reminder name, HistoryEvent in Payload). Unwrapping is keyed off the
// reminder name prefix so the rest of handleReminder treats both paths
// identically and error classification is shared.
func (a *activity) decodeReminderEvent(reminder *actorapi.Reminder) (*backend.HistoryEvent, error) {
	switch {
	case strings.HasPrefix(reminder.Name, common.ReminderPrefixXNSExec):
		var dispatch internalsv1pb.CrossNSDispatchRequest
		if err := reminder.Data.UnmarshalTo(&dispatch); err != nil {
			return nil, fmt.Errorf("failed to decode cross-ns activity reminder: %w", err)
		}
		var his backend.HistoryEvent
		if err := proto.Unmarshal(dispatch.GetPayload(), &his); err != nil {
			return nil, fmt.Errorf("failed to decode cross-ns activity payload: %w", err)
		}
		return &his, nil
	default:
		var his backend.HistoryEvent
		if err := reminder.Data.UnmarshalTo(&his); err != nil {
			return nil, fmt.Errorf("failed to decode activity reminder: %w", err)
		}
		return &his, nil
	}
}
