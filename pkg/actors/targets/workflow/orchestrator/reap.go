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

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// reapResolvedEscalation deletes the durable run-activity reminder an
// escalation armed for a task that resolved while the create was in flight:
// the execution's completion cannot delete a reminder it never knew existed,
// and the leaked fire would re-run the activity body.
func (o *orchestrator) reapResolvedEscalation(ctx context.Context, e *backend.HistoryEvent) {
	cctx, cancel := context.WithTimeout(ctx, redispatchCallTimeout)
	defer cancel()

	unlock, err := o.lock.ContextLock(cctx)
	if err != nil {
		return
	}
	resolved := false
	state, _, err := o.loadInternalState(cctx)
	if err == nil {
		resolved = state == nil || runtimestate.IsCompleted(o.rstate)
		if !resolved {
			resolved = true
			for _, u := range unresolvedScheduledTasks(state, o.foldEvents()) {
				if u.GetEventId() == e.GetEventId() {
					resolved = false
					break
				}
			}
		}
	}
	if resolved {
		delete(o.janitorEscalated, e.GetEventId())
	}
	unlock()
	if !resolved {
		return
	}

	appID := ""
	if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
		appID = router.GetTargetAppID()
	}
	o.reapEscalatedReminder(appID, e.GetEventId())
}

// reapEscalatedCompletions reaps the durable run-activity reminder of every
// escalated task the just-committed turn resolved, judged against the
// committed state rather than the turn's incoming events: a stale completion
// (e.g. a prior generation's) must not reap a live reminder, and the
// reminder identity (workflow ID and task ID) carries no generation. Marks
// from a previous generation are voided rather than reaped for the same
// reason: a delete could race the new generation's re-dispatch of the same
// task ID, and a leaked stale fire self-cleans (its completion is dropped
// as stale and the ack deletes the reminder). Runs under the turn lock,
// after the commit is durable.
func (o *orchestrator) reapEscalatedCompletions(state *wfenginestate.State) {
	if len(o.janitorEscalated) == 0 {
		return
	}
	if o.janitorRedispatchedGen != state.Generation {
		o.janitorEscalated = nil
		return
	}
	unresolved := make(map[int32]struct{})
	for _, u := range unresolvedScheduledTasks(state, o.foldEvents()) {
		unresolved[u.GetEventId()] = struct{}{}
	}
	for id, e := range o.janitorEscalated {
		if _, still := unresolved[id]; still {
			continue
		}
		delete(o.janitorEscalated, id)
		appID := ""
		if router := e.GetRouter(); router != nil && router.TargetAppID != nil {
			appID = router.GetTargetAppID()
		}
		o.reapEscalatedReminder(appID, id)
	}
}

// reapEscalatedReminder deletes a task's durable run-activity reminder,
// detached and best-effort (a failed delete leaves a fire the inflight cache
// absorbs on a warm host and the completion dedup absorbs at worst).
func (o *orchestrator) reapEscalatedReminder(appID string, id int32) {
	diag.DefaultWorkflowMonitoring.WorkflowLocalActivity(context.Background(), diag.StatusJanitorEscalationReaped)

	activityActorType := o.activityActorType
	if appID != "" && appID != o.appID {
		activityActorType = o.actorTypeBuilder.Activity(appID)
	}

	o.escLock.Lock()
	rootCtx := o.rootCtx
	if rootCtx.Err() != nil {
		o.escLock.Unlock()
		return
	}
	o.escWG.Add(1)
	o.escLock.Unlock()

	go func() {
		defer o.escWG.Done()
		cctx, cancel := context.WithTimeout(rootCtx, escalateTimeout)
		defer cancel()
		if derr := o.reminders.Delete(cctx, &actorapi.DeleteReminderRequest{
			Name:      todo.ActivityReminderName,
			ActorType: activityActorType,
			ActorID:   buildActivityActorID(o.actorID, id),
		}); derr != nil {
			if s, ok := grpcstatus.FromError(derr); !ok || s.Code() != codes.NotFound {
				log.Debugf("Workflow actor '%s': failed to reap escalated run-activity reminder for resolved task %d (a warm-host fire is absorbed by the inflight cache): %v", o.actorID, id, derr)
			}
			return
		}
		log.Debugf("Workflow actor '%s': reaped escalated run-activity reminder for resolved task %d", o.actorID, id)
	}()
}
