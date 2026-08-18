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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend"
)

// createWorkflowReminder schedules a wake-up reminder on the workflow actor.
// The reminderName is used verbatim: callers that want retries to collapse
// onto a single scheduler entry (overwrite-by-name) must pass a deterministic
// name (e.g. events.EventReminderName(prefix, event)); callers without a
// stable identity must build one with randomReminderName so unrelated
// reminders do not clobber each other.
func (o *orchestrator) createWorkflowReminder(ctx context.Context, reminderName string, data proto.Message, start time.Time, targetAppID string, concurrencyKey *string) error {
	actorType := o.actorTypeBuilder.Workflow(targetAppID)
	return o.createReminderWithType(ctx, reminderName, data, start, actorType, concurrencyKey)
}

// createWorkflowReminderForever is createWorkflowReminder with an unbounded
// (ctx-bounded) retry on Create. Use it only with a deterministic reminderName
// so retries are idempotent overwrites-by-name. It exists for the inbox
// wake-up (new-event) reminder, whose inbox row is already durable when this
// is called: a bounded give-up would strand that row with no driver.
func (o *orchestrator) createWorkflowReminderForever(ctx context.Context, reminderName string, data proto.Message, start time.Time, targetAppID string, concurrencyKey *string) error {
	actorType := o.actorTypeBuilder.Workflow(targetAppID)
	req, err := o.buildReminderRequest(reminderName, data, start, actorType, concurrencyKey)
	if err != nil {
		return err
	}
	return common.CreateReminderWithRetryForever(ctx, o.reminders, req)
}

// createRetentionReminder creates the retention reminder that triggers
// workflow purge. The name is deterministic so the call is idempotent: the
// scheduler's overwrite-by-name semantics ensure that retrying a Create
// after a transient scheduler failure converges on a single retention
// reminder rather than accumulating duplicates.
func (o *orchestrator) createRetentionReminder(ctx context.Context, name string, start time.Time) (string, error) {
	dueTime := start.UTC().Format(time.RFC3339Nano)

	return name, common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: o.retentionActorType,
		ActorID:   o.actorID,
		DueTime:   dueTime,
		Name:      name,
		// One shot, retry forever, jittered interval.
		FailurePolicy: common.RetryForeverPolicy(),
	})
}

// assertStartReminder creates (or overwrites by name) the deterministic start
// wake-up reminder for the ExecutionStarted event. The name is derived from
// the event's build-time timestamp (start-es-<unixnano>), so retries of the
// same server-side create collapse onto a single scheduler entry. A CLIENT
// retry of the same logical create regenerates the event timestamp, so
// callers re-driving a saved-but-never-run instance MUST pass the SAVED inbox
// event, not the incoming request's.
//
// The Create retry stays bounded (unlike assertNewEventReminder): the client
// call is blocked while this runs, and a bounded give-up is safe because the
// pending-start path in createIfCompleted lets any retry of the create
// re-assert this reminder from the saved event.
func (o *orchestrator) assertStartReminder(ctx context.Context, startEvent *backend.HistoryEvent) error {
	start := startEvent.GetTimestamp().AsTime()
	if ts := startEvent.GetExecutionStarted().GetScheduledStartTimestamp(); ts != nil {
		start = ts.AsTime()
	}

	workflowName := startEvent.GetExecutionStarted().GetName()
	reminderName := events.EventReminderName(reminderPrefixStart, startEvent)
	if err := o.createWorkflowReminder(ctx, reminderName, nil, start, o.appID, &workflowName); err != nil {
		return err
	}

	o.localDrive(reminderName, start, workflowName)
	return nil
}

// janitorReminderName is the per-instance repeating backstop reminder for the
// local-drive fast path. The "new-event" prefix is deliberate: old daprd
// binaries prefix-route any new-event* reminder to runWorkflowFromReminder,
// so a janitor firing against an instance owned by an older host still
// drives any pending inbox (mixed-version safety). It cannot collide with a
// real event reminder: those are always new-event-<code>-<id> with fixed
// short codes, never "janitor".
const janitorReminderName = "new-event-janitor"

// janitorPeriod resolves the janitor repeat interval once per process; shared
// with the activity target's stale-claim grace (see common.JanitorPeriod).
var janitorPeriod = common.JanitorPeriod

// driveNewEvent is the single dispatch point for waking the workflow after a
// durable inbox save. With the WorkflowsFastPath preview off it is
// exactly today's durable per-event reminder path. With it on, the per-event
// reminder (and its job upsert + delete commit pair) is elided: the turn is
// driven locally, backstopped by the per-instance janitor reminder plus
// on-failure escalation to the durable reminder (see wake.go).
//
// Durability first: if the janitor cannot be ensured, fall back to the
// durable per-event reminder path.
func (o *orchestrator) driveNewEvent(ctx context.Context, e *backend.HistoryEvent, state *wfenginestate.State) error {
	if !o.localWakeFastPath {
		return o.assertNewEventReminder(ctx, e, state)
	}

	if err := o.ensureJanitor(ctx, state); err != nil {
		log.Warnf("Workflow actor '%s': failed to ensure janitor reminder, falling back to a durable wake-up reminder: %v", o.actorID, err)
		return o.assertNewEventReminder(ctx, e, state)
	}

	dueTime := e.GetTimestamp().AsTime()
	if len(state.History) > 0 {
		dueTime = state.History[0].GetTimestamp().AsTime()
	}
	wfName := o.getExecutionStartedEvent(state).GetName()
	o.localDrive(events.EventReminderName(reminderPrefixNewEvent, e), dueTime, wfName)
	return nil
}

// ensureJanitor asserts the per-instance janitor reminder once per actor
// residency. Lazy per-residency (not per-instance-create) assertion means:
// instances that never take the fast path cost nothing, instances started on
// an old binary self-heal after migrating to a new one, and the create is
// idempotent (deterministic name, scheduler overwrite-by-name).
//
// The janitor repeats every janitorPeriod with a Drop failure policy: its
// periodicity IS its retry; a constant-retry policy would add ~1/s scheduler
// traffic whenever a fire lands behind a long turn. Its fire is a no-op
// (without deactivating) when the inbox is empty, drives a turn when inbox
// rows are pending, and self-deletes against terminal or purged instances
// (see runJanitor). It is deleted at the terminal turn and reaped by purge's
// DeleteByActorID on any binary version.
func (o *orchestrator) ensureJanitor(ctx context.Context, state *wfenginestate.State) error {
	if o.janitorAsserted.Load() {
		return nil
	}

	wfName := o.getExecutionStartedEvent(state).GetName()
	err := common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   o.actorID,
		Name:      janitorReminderName,
		DueTime:   time.Now().Add(janitorPeriod()).UTC().Format(time.RFC3339Nano),
		Period:    janitorPeriod().String(),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Drop{
				Drop: new(commonv1pb.JobFailurePolicyDrop),
			},
		},
		ConcurrencyKey: &wfName,
	})
	if err != nil {
		return err
	}

	o.janitorAsserted.Store(true)
	return nil
}

// deleteJanitor removes the janitor reminder. NotFound is tolerated: the
// janitor may never have been asserted (fast path never taken this
// residency), or an older binary may already have swept it via
// DeleteByActorID.
func (o *orchestrator) deleteJanitor(ctx context.Context) {
	if err := o.reminders.Delete(ctx, &actorapi.DeleteReminderRequest{
		Name:      janitorReminderName,
		ActorType: o.actorTypeBuilder.Workflow(o.appID),
		ActorID:   o.actorID,
	}); err != nil {
		if s, ok := grpcstatus.FromError(err); !ok || s.Code() != codes.NotFound {
			log.Debugf("Workflow actor '%s': failed to delete janitor reminder (it will self-delete on its next fire): %v", o.actorID, err)
		}
	}
}

// assertNewEventReminder creates (or overwrites by name) the deterministic
// new-event wake-up reminder for the workflow actor that holds e in its inbox.
func (o *orchestrator) assertNewEventReminder(ctx context.Context, e *backend.HistoryEvent, state *wfenginestate.State) error {
	dueTime := e.Timestamp.AsTime()
	if len(state.History) > 0 {
		dueTime = state.History[0].Timestamp.AsTime()
	}
	wfName := o.getExecutionStartedEvent(state).GetName()
	reminderName := events.EventReminderName(reminderPrefixNewEvent, e)
	// Retry the Create forever (bounded by the actor context): the inbox event
	// was saved before this call, so giving up after a bounded budget would
	// leave a durable inbox row with no wake-up reminder to drive it. The
	// reminder name is deterministic, so repeated Creates collapse onto a
	// single scheduler entry. This is the workflow-actor-side durability that
	// external events (RaiseEvent) lack on the sender side, unlike activity
	// results.
	if err := o.createWorkflowReminderForever(ctx, reminderName, nil, dueTime, o.appID, &wfName); err != nil {
		return err
	}

	o.localDrive(reminderName, dueTime, wfName)
	return nil
}

// randomReminderName returns the prefix with a random suffix appended.
// Use for reminders that have no stable identity to deduplicate retries by.
func randomReminderName(prefix string) (string, error) {
	b := make([]byte, 6)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("failed to generate reminder ID: %w", err)
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(b), nil
}

func (o *orchestrator) createReminderWithType(ctx context.Context, reminderName string, data proto.Message, start time.Time, actorType string, concurrencyKey *string) error {
	req, err := o.buildReminderRequest(reminderName, data, start, actorType, concurrencyKey)
	if err != nil {
		return err
	}
	return common.CreateReminderWithRetry(ctx, o.reminders, req)
}

// buildReminderRequest assembles the CreateReminderRequest shared by the
// bounded (createReminderWithType) and unbounded (createWorkflowReminderForever)
// create paths.
func (o *orchestrator) buildReminderRequest(reminderName string, data proto.Message, start time.Time, actorType string, concurrencyKey *string) (*actorapi.CreateReminderRequest, error) {
	dueTime := start.UTC().Format(time.RFC3339Nano)

	var adata *anypb.Any
	if data != nil {
		var err error
		adata, err = anypb.New(data)
		if err != nil {
			return nil, err
		}
	}

	log.Debugf("Workflow actor '%s||%s': creating '%s' reminder with DueTime = '%s'", actorType, o.actorID, reminderName, dueTime)

	return &actorapi.CreateReminderRequest{
		ActorType: actorType,
		ActorID:   o.actorID,
		Data:      adata,
		DueTime:   dueTime,
		Name:      reminderName,
		// One shot, retry forever, jittered interval.
		FailurePolicy:  common.RetryForeverPolicy(),
		ConcurrencyKey: concurrencyKey,
	}, nil
}

// deleteAllReminders deletes all reminders for the workflow and its
// activities. This is called when the workflow completes to ensure no orphan
// reminders (e.g. unfired timers) remain in the scheduler.
func (o *orchestrator) deleteAllReminders(ctx context.Context) error {
	actorType := o.actorTypeBuilder.Workflow(o.appID)

	log.Debugf("Workflow actor '%s': deleting all reminders for completed workflow", o.actorID)

	if err := o.reminders.DeleteByActorID(ctx, &actorapi.DeleteRemindersByActorIDRequest{
		ActorType:       actorType,
		ActorID:         o.actorID,
		MatchIDAsPrefix: false,
	}); err != nil {
		return fmt.Errorf("actor '%s' failed to delete reminders on completion: %w", o.actorID, err)
	}

	if err := o.reminders.DeleteByActorID(ctx, &actorapi.DeleteRemindersByActorIDRequest{
		ActorType:       o.activityActorType,
		ActorID:         o.actorID + "::",
		MatchIDAsPrefix: true,
	}); err != nil {
		return fmt.Errorf("actor '%s' failed to delete activity reminders on completion: %w", o.actorID, err)
	}

	return nil
}
