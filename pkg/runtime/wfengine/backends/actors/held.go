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

package actors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

// Held operations (the WorkflowsHeldOperations preview): while no daprd of
// this app hosts its workflow actor type, which is the case while none has a
// workflow worker connected, a start or a new event aimed at this app is held
// as a scheduler reminder on the workflow actor instead of blocking or
// failing. The first host to come up replays it (orchestrator/held.go).
//
// Only operations aimed at this app are held: the scheduler refuses a
// reminder on another app's workflow actor.

// canHold reports whether an operation aimed at actorType may be held.
func (abe *Actors) canHold(actorType string) bool {
	return abe.workflowsHeldOperations && actorType == abe.workflowActorType
}

// noHost reports whether an operation aimed at actorType should be held
// because placement has no host for the actor. Checking before the call
// avoids the actor router's own retries on a missing host, which would delay
// every held operation by several seconds.
func (abe *Actors) noHost(ctx context.Context, actorType, actorID string) bool {
	if !abe.canHold(actorType) {
		return false
	}

	placement, err := abe.actors.Placement(ctx)
	if err != nil {
		return false
	}

	_, _, cancel, err := placement.LookupActor(ctx, &actorsapi.LookupActorRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})
	if cancel != nil {
		cancel(nil)
	}
	return errors.Is(err, messages.ErrActorNoAddress)
}

// isNoHost reports whether err from an actor call aimed at actorType means
// the operation should be held: the host went away between the placement
// check and the call.
func (abe *Actors) isNoHost(actorType string, err error) bool {
	return abe.canHold(actorType) && errors.Is(err, messages.ErrActorNoAddress)
}

// holdStart holds a workflow start until a host of this app's workflow actor
// type comes up.
func (abe *Actors) holdStart(ctx context.Context, instanceID string, req *backend.CreateWorkflowInstanceRequest, start time.Time) error {
	// The replay measures the hold against the start event's timestamp.
	if e := req.GetStartEvent(); e.GetTimestamp() == nil {
		e.Timestamp = timestamppb.Now()
	}

	err := abe.hold(ctx, instanceID, common.ReminderNameHeldStart, req)
	abe.recordHold(ctx, diag.CreateWorkflow, start, err)
	if err != nil {
		return fmt.Errorf("failed to hold the start of workflow '%s' while no worker is connected: %w", instanceID, err)
	}

	log.Debugf("Workflow '%s': no worker is connected, holding its start until one connects", instanceID)
	return nil
}

// holdEvent holds a new event for a workflow instance until a host of this
// app's workflow actor type comes up.
func (abe *Actors) holdEvent(ctx context.Context, id api.InstanceID, e *backend.HistoryEvent, start time.Time) error {
	// The replay measures the hold against the event's timestamp.
	if e.GetTimestamp() == nil {
		e.Timestamp = timestamppb.Now()
	}

	err := abe.hold(ctx, string(id), common.ReminderPrefixHeldEvent+uuid.NewString(), e)
	abe.recordHold(ctx, diag.AddEvent, start, err)
	if err != nil {
		return fmt.Errorf("failed to hold an event for workflow '%s' while no worker is connected: %w", id, err)
	}

	log.Debugf("Workflow '%s': no worker is connected, holding a %T event until one connects", id, e.GetEventType())
	return nil
}

// hold creates the reminder that carries a held operation. It goes straight
// to the scheduler: the actor reminders API refuses an actor type that this
// daprd does not host, which is always the case here.
func (abe *Actors) hold(ctx context.Context, actorID, name string, data proto.Message) error {
	adata, err := anypb.New(data)
	if err != nil {
		return err
	}

	reminders, err := abe.actors.Reminders(ctx)
	if err != nil {
		return err
	}

	sched, err := reminders.Scheduler()
	if err != nil {
		return err
	}

	return common.CreateReminderWithRetry(ctx, sched, &actorsapi.CreateReminderRequest{
		ActorType:     abe.workflowActorType,
		ActorID:       actorID,
		Name:          name,
		Data:          adata,
		DueTime:       time.Now().UTC().Format(time.RFC3339Nano),
		FailurePolicy: common.HeldOperationPolicy(),
	})
}

func (abe *Actors) recordHold(ctx context.Context, operation string, start time.Time, err error) {
	status := diag.StatusHeld
	if err != nil {
		status = diag.StatusFailed
	}
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, operation, status, diag.ElapsedSince(start))
}
