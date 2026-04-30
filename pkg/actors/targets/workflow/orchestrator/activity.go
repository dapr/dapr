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
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callActivities(ctx context.Context, es []*backend.HistoryEvent, state *wfenginestate.State, outgoingHistory map[int32]*protos.PropagatedHistory) dispatchResult {
	var dueTime time.Time
	if len(state.History) > 0 {
		dueTime = state.History[0].GetTimestamp().AsTime()
	} else {
		dueTime = state.Inbox[0].GetTimestamp().AsTime()
	}

	var result dispatchResult
	for _, e := range es {
		err := o.callActivity(ctx, e, dueTime, state.Generation, outgoingHistory[e.GetEventId()])
		if err != nil {
			if errors.Is(err, todo.ErrDuplicateInvocation) {
				log.Warnf("Workflow actor '%s': activity invocation '%s::%d' was flagged as a duplicate and will be skipped", o.actorID, e.GetTaskScheduled().GetName(), e.GetEventId())
				continue
			}

			result.recordFailure(e.GetEventId(), err)
			continue
		}
	}

	return result
}

func (o *orchestrator) callActivity(ctx context.Context, e *backend.HistoryEvent, dueTime time.Time, generation uint64, ph *protos.PropagatedHistory) error {
	ts := e.GetTaskScheduled()
	if ts == nil {
		log.Warnf("Workflow actor '%s': unable to process task '%v'", o.actorID, e)
		return nil
	}

	if err := o.activityPayloadOversize(e, ph); err != nil {
		return err
	}

	// Only wrap in the ActivityInvocation envelope when there is propagated history to carry.
	var payload proto.Message = e
	if ph != nil {
		if o.signer == nil {
			log.Warnf("Workflow actor '%s': propagating unsigned workflow history to activity '%s::%d' (signing is not configured; chunks cannot be cryptographically verified by the receiver)", o.actorID, ts.GetName(), e.GetEventId())
		}
		payload = &protos.ActivityInvocation{
			HistoryEvent:      e,
			PropagatedHistory: ph,
		}
	}

	invocationData, err := proto.Marshal(payload)
	if err != nil {
		return err
	}

	activityActorType := o.activityActorType
	if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
		activityActorType = o.actorTypeBuilder.Activity(router.GetTargetAppID())
	}

	targetActorID := buildActivityActorID(o.actorID, e.GetEventId(), generation)

	o.activityResultAwaited.Store(true)

	log.Debugf("Workflow actor '%s': invoking execute method on activity actor '%s||%s'", o.actorID, activityActorType, targetActorID)

	ctx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()

	_, err = o.router.Call(ctx, internalsv1pb.
		NewInternalInvokeRequest("Execute").
		WithActor(activityActorType, targetActorID).
		WithMetadata(map[string][]string{
			todo.MetadataActivityReminderDueTime: {strconv.FormatInt(dueTime.UnixMilli(), 10)},
		}).
		WithData(invocationData).
		WithContentType(invokev1.ProtobufContentType),
	)
	if err != nil {
		// If the call was denied by a workflow access policy, fail the
		// activity task immediately rather than retrying.
		if isPermissionDenied(err) {
			log.Errorf("Workflow actor '%s': activity '%s' denied by workflow access policy: %v", o.actorID, ts.GetName(), err)
			return o.failActivityACL(ctx, e)
		}

		if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
			return fmt.Errorf("failed to dispatch activity '%s' to remote app '%s' (the app may not be available): %w", ts.GetName(), router.GetTargetAppID(), err)
		}

		return fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %w", targetActorID, ts.GetName(), err)
	}

	return nil
}

// failActivityACL creates a TaskFailed event on the parent orchestrator when
// the activity call is rejected by a WorkflowAccessPolicy. Uses a reminder to
// deliver the event in a fresh execution cycle.
func (o *orchestrator) failActivityACL(ctx context.Context, e *backend.HistoryEvent) error {
	failedEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(time.Now()),
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{
				TaskScheduledId: e.GetEventId(),
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    "WorkflowAccessPolicyDenied",
					ErrorMessage: "access denied by workflow access policy",
				},
			},
		},
	}

	if _, err := o.createWorkflowReminder(ctx, common.ReminderPrefixActivityResult, failedEvent, time.Now(), o.appID, nil); err != nil {
		return fmt.Errorf("failed to create activity failure reminder: %w", err)
	}

	return nil
}

func buildActivityActorID(workflowID string, taskID int32, generation uint64) string {
	return workflowID + "::" + strconv.Itoa(int(taskID)) + "::" + strconv.FormatUint(generation, 10)
}
