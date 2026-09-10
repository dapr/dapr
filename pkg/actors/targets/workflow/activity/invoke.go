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
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (a *activity) handleInvoke(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	method := req.GetMessage().GetMethod()
	data := req.GetMessage().GetData().GetValue()

	if err := a.checkAccessPolicy(method, data, req.GetMetadata()); err != nil {
		return nil, err
	}

	dueTime := time.Now()
	if s, ok := req.GetMetadata()[todo.MetadataActivityReminderDueTime]; ok && len(s.GetValues()) > 0 {
		unix, err := strconv.ParseInt(s.GetValues()[0], 10, 64)
		if err != nil {
			return nil, err
		}
		dueTime = time.UnixMilli(unix)
	}

	log.Debugf("Activity actor '%s': invoking method '%s'", a.actorID, method)

	invocation, activityName, err := decodeActivityInvocation(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode activity invocation: %w", err)
	}

	// A janitor re-dispatch may race a body still live on the previous
	// placement owner, so it is gated on the durable execution-claim record
	// before a drive is armed. A live local entry acks without a state read:
	// it owns delivery, and a drive joining it would re-execute through the
	// ungated retry once it settles with the defer error. A stranded entry
	// (delivery lost, not held) falls through so the execution path's stale
	// eviction re-executes; acking it would also swallow the escalation that
	// creates the durable reminder.
	if a.fastPath && metaFlagged(req, todo.MetadataActivityJanitorRedispatch) {
		workflowID, err := a.workflowID()
		if err != nil {
			return nil, err
		}
		taskEvent := invocation.GetHistoryEvent()
		key := inflight.Key(a.actorID, taskEvent)
		if call, ok := a.inflight.Peek(key); ok && !a.staleClaim(call, workflowID, taskEvent.GetEventId()) {
			return nil, nil
		}
		if proceed, gerr := a.gate(ctx, key, nil); !proceed {
			return nil, gerr
		}
	}

	// Fast path: when the dispatching orchestrator certifies its janitor
	// backstop is armed and this host runs the preview, drive the execution
	// locally instead of creating the durable run-activity reminder, eliding
	// its job upsert/delete commit pair and the scheduler trigger round trip.
	// Delayed executions keep the scheduler path so the delay is honoured; a
	// drive that cannot be armed (factory halting) falls through to the
	// durable reminder.
	if a.fastPath && metaFlagged(req, todo.MetadataActivityLocalDrive) && !dueTime.After(time.Now()) {
		if a.localDrive(invocation, activityName) {
			return nil, nil
		}
	}

	return nil, a.createActivityReminder(ctx, a.actorID, invocation, dueTime, activityName)
}

// metaFlagged reports whether the dispatching orchestrator set the given
// boolean marker on this Execute call: MetadataActivityJanitorRedispatch marks
// a re-dispatch of an unresolved task, MetadataActivityLocalDrive certifies
// that a janitor is watching this activity (without it the durable reminder
// must be kept: the orchestrator may be an older or gate-off binary).
func metaFlagged(req *internalsv1pb.InternalInvokeRequest, key string) bool {
	v, ok := req.GetMetadata()[key]
	return ok && len(v.GetValues()) > 0 && v.GetValues()[0] == "true"
}

// decodeActivityInvocation parses an activity invocation payload. New
// orchestrators wrap the HistoryEvent in an ActivityInvocation envelope
// (which may carry PropagatedHistory) only when propagation is present, and
// otherwise send a raw HistoryEvent for rolling-upgrade compatibility with
// older daprds. The envelope is tried first.
func decodeActivityInvocation(data []byte) (*protos.ActivityInvocation, *string, error) {
	var invocation protos.ActivityInvocation
	envelopeErr := proto.Unmarshal(data, &invocation)
	if envelopeErr == nil && invocation.GetHistoryEvent() != nil {
		return &invocation, taskScheduledName(invocation.GetHistoryEvent()), nil
	}

	// TODO: remove this legacy fallback in v1.19, once the floor version is
	// past the rollout.
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

	// TODO: remove the legacy raw HistoryEvent fallback in v1.19 once reminders
	// written by pre-propagation daprds have been drained from the rollout.
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

	err := a.executeActivity(ctx, reminder, &invocation)
	if err == nil {
		return nil
	}

	// Every failure is returned as-is so the reminder is retried in the next
	// period interval; the classification only picks the log line.
	switch {
	case errors.Is(err, context.Canceled):
		log.Warnf("%s: received cancellation signal while waiting for activity execution '%s'", a.actorID, reminder.Name)
	case errors.Is(err, context.DeadlineExceeded), wferrors.IsRecoverable(err):
		log.Warnf("%s: execution of '%s' failed with a recoverable error and will be retried later: %v", a.actorID, reminder.Name, err)
	default:
		log.Errorf("%s: execution failed with an error: %v", a.actorID, err)
	}
	return err
}
