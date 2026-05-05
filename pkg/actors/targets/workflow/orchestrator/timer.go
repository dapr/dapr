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
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) createTimers(ctx context.Context, es []*backend.HistoryEvent, generation uint64) error {
	for _, e := range es {
		if err := o.createTimer(ctx, e, generation); err != nil {
			return err
		}
	}

	return nil
}

// hasUnfiredTimers returns true if the runtime state contains TimerCreated
// events that do not have a corresponding TimerFired event.
func hasUnfiredTimers(rs *protos.WorkflowRuntimeState) bool {
	var created, fired int
	for _, events := range [2][]*protos.HistoryEvent{
		(rs.GetOldEvents()),
		(rs.GetNewEvents()),
	} {
		for _, e := range events {
			if e.GetTimerCreated() != nil {
				created++
			} else if e.GetTimerFired() != nil {
				fired++
			}
		}
	}
	return created > fired
}

// timerReminderName returns the deterministic reminder name for a timer with
// the given ID. Timer reminders use deterministic names (no random suffix) so
// they can be reliably deleted when no longer needed.
func timerReminderName(timerID int32) string {
	return reminderPrefixTimer + strconv.Itoa(int(timerID))
}

func (o *orchestrator) createTimer(ctx context.Context, e *backend.HistoryEvent, generation uint64) error {
	ts := e.GetTimerFired()
	if ts == nil {
		return errors.New("invalid timer object for creating a timer reminder")
	}

	start := e.GetTimerFired().GetFireAt().AsTime()
	reminderName := timerReminderName(e.GetTimerFired().GetTimerId())
	data := &backend.DurableTimer{TimerEvent: e, Generation: generation}

	log.Debugf("Workflow actor '%s': creating reminder '%s' for the durable timer, duetime=%s", o.actorID, reminderName, start)

	if err := o.createTimerReminder(ctx, reminderName, data, start); err != nil {
		return fmt.Errorf("actor '%s' failed to create reminder for timer: %w", o.actorID, err)
	}

	return nil
}

func (o *orchestrator) createTimerReminder(ctx context.Context, name string, data proto.Message, start time.Time) error {
	actorType := o.actorTypeBuilder.Workflow(o.appID)
	dueTime := start.UTC().Format(time.RFC3339)

	adata, err := anypb.New(data)
	if err != nil {
		return err
	}

	log.Debugf("Workflow actor '%s||%s': creating '%s' reminder with DueTime = '%s'", actorType, o.actorID, name, dueTime)

	return common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: actorType,
		ActorID:   o.actorID,
		Data:      adata,
		DueTime:   dueTime,
		Name:      name,
		// One shot, retry forever, every second.
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
	})
}

// deleteCancelledEventTimers scans the workflow history to find timer reminders
// associated with WaitForSingleEvent calls where the event has been received
// before the timer fired, and deletes those now-unnecessary timer reminders.
// 1. Find all TimerCreated events associated with external events (origin.external_event or legacy Name field)
// 2. Remove any that have already fired (matching TimerFired events)
// 3. For each new EventRaised, consume one matching unfired timer (FIFO order)
// 4. Delete the timer reminders for consumed timers
//
// Only EventRaised events in NewEvents are considered as triggers for
// cancellation, since events in OldEvents were already processed in a
// previous execution. TimerCreated and TimerFired are scanned across both
// OldEvents and NewEvents.
func (o *orchestrator) deleteCancelledEventTimers(ctx context.Context, rs *protos.WorkflowRuntimeState) error {
	newEvents := rs.GetNewEvents()

	// Quick check: if there are no new events that could contain an
	// EventRaised, there's nothing to cancel.
	hasEventRaised := false
	for _, e := range newEvents {
		if e.GetEventRaised() != nil {
			hasEventRaised = true
			break
		}
	}
	if !hasEventRaised {
		return nil
	}

	// Build the set of unfired event-associated timers from full history.
	// pendingEventTimers: event name (uppercase) -> list of timer IDs (creation order)
	pendingEventTimers := make(map[string][]int32)
	firedTimerIDs := make(map[int32]bool)

	for _, events := range [2][]*protos.HistoryEvent{
		rs.GetOldEvents(),
		newEvents,
	} {
		for _, e := range events {
			if tc := e.GetTimerCreated(); tc != nil {
				var name *string
				if ee := tc.GetExternalEvent(); ee != nil {
					name = new(ee.GetName())
				} else if tc.Name != nil && tc.GetOrigin() == nil {
					// TODO: We're doing `Name` matching for backwards compatibility.
					// By around v1.19 we should only match with
					// `origin.external_event` and remove the fallback logic.
					name = new(tc.GetName())
				}
				if name != nil {
					key := strings.ToUpper(*name)
					pendingEventTimers[key] = append(pendingEventTimers[key], e.GetEventId())
				}
			} else if tf := e.GetTimerFired(); tf != nil {
				firedTimerIDs[tf.GetTimerId()] = true
			}
		}
	}

	// Remove already-fired timers from the pending map.
	for key, timerIDs := range pendingEventTimers {
		filtered := timerIDs[:0]
		for _, id := range timerIDs {
			if !firedTimerIDs[id] {
				filtered = append(filtered, id)
			}
		}
		if len(filtered) == 0 {
			delete(pendingEventTimers, key)
		} else {
			pendingEventTimers[key] = filtered
		}
	}

	// For each new EventRaised, consume one matching unfired timer (FIFO).
	var cancelledTimerIDs []int32
	for _, e := range newEvents {
		if er := e.GetEventRaised(); er != nil {
			key := strings.ToUpper(er.GetName())
			if timerIDs, ok := pendingEventTimers[key]; ok && len(timerIDs) > 0 {
				cancelledTimerIDs = append(cancelledTimerIDs, timerIDs[0])
				if len(timerIDs) == 1 {
					delete(pendingEventTimers, key)
				} else {
					pendingEventTimers[key] = timerIDs[1:]
				}
			}
		}
	}

	if len(cancelledTimerIDs) == 0 {
		return nil
	}

	// Delete the timer reminders.
	actorType := o.actorTypeBuilder.Workflow(o.appID)
	for _, timerID := range cancelledTimerIDs {
		name := timerReminderName(timerID)
		log.Debugf("Workflow actor '%s': deleting cancelled event timer reminder '%s'", o.actorID, name)
		if err := o.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
			Name:      name,
			ActorType: actorType,
			ActorID:   o.actorID,
		}); err != nil {
			if s, ok := grpcstatus.FromError(err); ok && s.Code() == codes.NotFound {
				log.Debugf("Workflow actor '%s': timer reminder '%s' already deleted, ignoring", o.actorID, name)
				continue
			}
			return fmt.Errorf("actor '%s' failed to delete cancelled timer reminder '%s': %w", o.actorID, name, err)
		}
	}

	return nil
}
