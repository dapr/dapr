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
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	orcherrors "github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// failParentDetachedWorkflowACL marks the parent orchestration as
// terminally FAILED in response to a detached spawn that was rejected by
// the target app's WorkflowAccessPolicy. The orchestrator function may
// have already staged an ExecutionCompletedEvent (success) for this run;
// we override it with a FAILED variant so the persisted terminal state
// reflects the fact that the orchestration could not fulfill its
// scheduling intent. Returns nil so the caller can mark the reminder as
// completed and stop retrying — the denial is permanent.
func (o *orchestrator) failParentDetachedWorkflowACL(ctx context.Context, state *wfenginestate.State, rs *backend.WorkflowRuntimeState, denyErr *orcherrors.DetachedSpawnDeniedError) error {
	completed := events.NewExecutionCompletedFailedEvent("WorkflowAccessPolicyDenied", denyErr.Error())
	rs.CompletedEvent = completed
	rs.CompletedTime = timestamppb.Now()

	// Replace the orchestrator's pending ExecutionCompletedEvent (if any)
	// with the FAILED variant so caller history records the same terminal
	// status that rs.CompletedEvent points to. If the orchestrator did not
	// emit a terminal event this run (rare — only with mid-flight dispatch
	// of a fresh-state remote action), append one so the workflow lands in
	// a terminal state instead of stalling.
	var replaced bool
	for i := range rs.NewEvents {
		if rs.NewEvents[i].GetExecutionCompleted() != nil {
			rs.NewEvents[i] = &protos.HistoryEvent{
				EventId:   rs.NewEvents[i].GetEventId(),
				Timestamp: rs.NewEvents[i].GetTimestamp(),
				EventType: &protos.HistoryEvent_ExecutionCompleted{
					ExecutionCompleted: completed,
				},
			}
			replaced = true
			break
		}
	}
	if !replaced {
		rs.NewEvents = append(rs.NewEvents, &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionCompleted{
				ExecutionCompleted: completed,
			},
		})
	}

	state.ApplyRuntimeStateChanges(rs)
	if err := o.signAndSaveState(ctx, state); err != nil {
		return fmt.Errorf("failed to persist terminal failure for denied detached spawn: %w", err)
	}

	log.Warnf("Workflow actor '%s': failing parent terminally because detached workflow spawn was denied by access policy: %v", o.actorID, denyErr)
	return nil
}
