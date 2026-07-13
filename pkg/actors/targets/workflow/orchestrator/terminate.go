/*
Copyright 2026 The Dapr Authors
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
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// remoteChildTerminateTimeout bounds the cross-app delivery to a remote
// child. This runs under the parent's actor lock while a terminating child
// may be calling back into the parent, so the delivery must not block
// indefinitely; the empty-inbox retry path redelivers.
const remoteChildTerminateTimeout = 5 * time.Second

// terminateChildren delivers a recursive ExecutionTerminated to every child
// recorded in history. Called only after the terminal state has been
// persisted, so a delivery failure can never roll the parent back; it is
// re-derived from history and idempotent, so failures are retried via the
// wake-up reminder through the empty-inbox path. No-op unless the workflow
// was terminated with Recurse=true.
func (o *orchestrator) terminateChildren(ctx context.Context, state *wfenginestate.State) error {
	var term *protos.ExecutionTerminatedEvent
	for _, e := range state.History {
		if et := e.GetExecutionTerminated(); et != nil {
			term = et
			break
		}
	}
	if term == nil || !term.GetRecurse() {
		return nil
	}

	var errs []error
	for _, child := range collectChildren(state.History) {
		var err error
		if child.targetAppID != "" && child.targetAppID != o.appID {
			err = o.terminateRemoteChild(ctx, child, term)
		} else {
			err = o.createCascadeTerminateReminder(ctx, child, term)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("child %q: %w", child.instanceID, err))
		}
	}
	return errors.Join(errs...)
}

// createCascadeTerminateReminder creates the cascade-terminate reminder on a
// same-app child's workflow actor, carrying the ExecutionTerminated event as
// data. A reminder rather than an actor invocation so no actor lock is taken
// under the parent's lock, and delivery keeps retrying while the child is
// unavailable.
func (o *orchestrator) createCascadeTerminateReminder(ctx context.Context, child childRef, term *protos.ExecutionTerminatedEvent) error {
	data, err := anypb.New(cascadeTerminateEvent(term))
	if err != nil {
		return err
	}

	return common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: o.actorType,
		ActorID:   child.instanceID,
		Name:      reminderCascadeTerminate,
		Data:      data,
		DueTime:   time.Now().UTC().Format(time.RFC3339Nano),
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

// terminateRemoteChild delivers the ExecutionTerminated to a cross-app child
// via AddWorkflowEvent, since reminders cannot be created for non-hosted
// actor types.
func (o *orchestrator) terminateRemoteChild(ctx context.Context, child childRef, term *protos.ExecutionTerminatedEvent) error {
	data, err := proto.Marshal(cascadeTerminateEvent(term))
	if err != nil {
		return err
	}

	cctx, cancel := context.WithTimeout(ctx, remoteChildTerminateTimeout)
	defer cancel()

	_, err = o.router.Call(cctx, internalsv1pb.
		NewInternalInvokeRequest(todo.AddWorkflowEventMethod).
		WithActor(o.actorTypeBuilder.Workflow(child.targetAppID), child.instanceID).
		WithData(data).
		WithContentType(invokev1.OctetStreamContentType))
	return err
}

func cascadeTerminateEvent(term *protos.ExecutionTerminatedEvent) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{
				Input:   term.GetInput(),
				Recurse: true,
			},
		},
	}
}
