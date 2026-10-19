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
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

// Held operations (the WorkflowsHeldOperations preview): while no daprd of
// this app hosts its workflow actor type, which is the case while none has a
// workflow worker connected, a start or a new event aimed at this app is held
// as a scheduler reminder on the workflow actor instead of blocking or
// failing. The first host to come up replays it (orchestrator/held.go).
//
// Only operations aimed at this app are held: the scheduler refuses a
// reminder on another app's workflow actor.

// failFastIfHoldable returns ctx set up for a call aimed at actorType: when
// the operation may be held, the actor router fails at once with
// ErrActorNoAddress on a missing host, instead of retrying for several
// seconds, so the caller can hold it.
//
// An operation is held only once placement is ready: before its first table,
// a daprd cannot tell a missing host from one it has not heard of yet.
func (abe *Actors) failFastIfHoldable(ctx context.Context, actorType string) context.Context {
	if !abe.workflowsHeldOperations || actorType != abe.workflowActorType {
		return ctx
	}

	placement, err := abe.actors.Placement(ctx)
	if err != nil || !placement.Ready() {
		return ctx
	}

	return router.WithFailFastNoAddress(ctx)
}

// isNoHost reports whether err from an actor call made with a ctx from
// failFastIfHoldable means the operation should be held: placement has no
// host for the actor.
func (abe *Actors) isNoHost(ctx context.Context, err error) bool {
	return router.IsFailFastNoAddress(ctx) && errors.Is(err, messages.ErrActorNoAddress)
}

type heldMarkerKey struct{}

// WithHeldMarker returns ctx carrying a marker that records whether an
// operation made with it was held. WatchWorkflowRuntimeStatus returns at
// once under a marked ctx: a held operation has nothing to wait for until a
// worker connects.
func WithHeldMarker(ctx context.Context) context.Context {
	return context.WithValue(ctx, heldMarkerKey{}, new(atomic.Bool))
}

func markHeld(ctx context.Context) {
	if m, ok := ctx.Value(heldMarkerKey{}).(*atomic.Bool); ok {
		m.Store(true)
	}
}

func wasHeld(ctx context.Context) bool {
	m, ok := ctx.Value(heldMarkerKey{}).(*atomic.Bool)
	return ok && m.Load()
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

	markHeld(ctx)
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

	markHeld(ctx)
	log.Debugf("Workflow '%s': no worker is connected, holding a %T event until one connects", id, e.GetEventType())
	return nil
}

// hold creates the reminder that carries a held operation. It goes straight
// to the scheduler: the actor reminders API refuses an actor type that this
// daprd does not host, which is always the case here.
//
// The reminder never overwrites one of the same name: a second held start of
// an instance that differs from the first (another workflow name or input)
// must not silently replace the start its caller was already told was
// accepted. The first held start wins, as the first create wins when a worker
// is connected; CreateReminderWithRetry treats the AlreadyExists as created.
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
		Overwrite:     ptr.Of(false),
	})
}

func (abe *Actors) recordHold(ctx context.Context, operation string, start time.Time, err error) {
	status := diag.StatusHeld
	if err != nil {
		status = diag.StatusFailed
	}
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, operation, status, diag.ElapsedSince(start))
}
