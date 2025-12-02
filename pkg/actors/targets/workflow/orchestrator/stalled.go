package orchestrator

import (
	"context"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/ptr"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func handlePatchMismatch(ctx context.Context, o *orchestrator, state *wfenginestate.State, rs *backend.OrchestrationRuntimeState) (todo.RunCompleted, error) {
	// We need to clear the completed event and time so that the workflow is not considered completed.
	rs.CompletedEvent = nil
	rs.CompletedTime = nil

	// Since we don't allow the workflow to be completed, we need to filter out the completed events so that the workflow is not moved to the completed state.
	filteredNewEvents := make([]*protos.HistoryEvent, 0, len(rs.NewEvents))
	for _, e := range rs.NewEvents {
		if e.GetExecutionCompleted() == nil {
			filteredNewEvents = append(filteredNewEvents, e)
		}
	}
	rs.NewEvents = filteredNewEvents

	_ = runtimestate.AddEvent(rs, &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStalled{
			ExecutionStalled: &protos.ExecutionStalledEvent{
				Reason: protos.StalledReason_PATCH_MISMATCH,
				// TODO: Return the actual patches that are mismatched
				Description: ptr.Of("Patch mismatch"),
			},
		},
	})

	log.Warnf("Workflow actor '%s': workflow is stalled, skipping timer/activity processing", o.actorID)
	state.ApplyRuntimeStateChanges(rs)
	err := o.saveInternalState(ctx, state)
	if err != nil {
		return todo.RunCompletedFalse, err
	}
	log.Infof("Workflow actor '%s': workflow is stalled; holding reminder until context is canceled", o.actorID)
	<-ctx.Done()
	return todo.RunCompletedFalse, ctx.Err()
}

func hasPatchMismatch(rs *backend.OrchestrationRuntimeState) bool {
	seen := make(map[string]struct{})
	var historyPatches []string
	for _, e := range rs.OldEvents {
		if os := e.GetOrchestratorStarted(); os != nil {
			if version := os.GetVersion(); version != nil {
				for _, p := range version.GetPatches() {
					if _, ok := seen[p]; !ok {
						seen[p] = struct{}{}
						historyPatches = append(historyPatches, p)
					}
				}
			}
		}
	}

	if len(historyPatches) == 0 {
		return false
	}

	var currentPatches []string
	for _, e := range rs.NewEvents {
		if os := e.GetOrchestratorStarted(); os != nil {
			if version := os.GetVersion(); version != nil {
				currentPatches = version.GetPatches()
			}
			break
		}
	}

	if len(historyPatches) != len(currentPatches) {
		return true
	}

	for i, patch := range historyPatches {
		if currentPatches[i] != patch {
			return true
		}
	}

	return false
}
