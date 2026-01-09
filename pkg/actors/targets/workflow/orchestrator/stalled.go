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
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/ptr"
)

func (o *orchestrator) isStalled(ctx context.Context, state *wfenginestate.State, rs *backend.OrchestrationRuntimeState) (bool, error) {
	historyPatches := collectAllPatches(rs.OldEvents)
	currentPatches := getLastPatches(rs.NewEvents)
	hasMismatch, description := processPatchMismatch(historyPatches, currentPatches)
	if !hasMismatch {
		return false, nil
	}

	rs.CompletedEvent = nil
	rs.CompletedTime = nil

	hasFilteredNewEvents := len(rs.NewEvents) > 0
	rs.NewEvents = []*protos.HistoryEvent{}

	lastEvent := rs.OldEvents[len(rs.OldEvents)-1]
	hasStalledEvent := false
	if execStalledEvent := lastEvent.GetExecutionStalled(); execStalledEvent == nil || execStalledEvent.GetDescription() != description {
		hasStalledEvent = true
		_ = runtimestate.AddEvent(rs, &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionStalled{
				ExecutionStalled: &protos.ExecutionStalledEvent{
					Reason:      protos.StalledReason_PATCH_MISMATCH,
					Description: ptr.Of(description),
				},
			},
		})
	}
	if hasFilteredNewEvents || hasStalledEvent {
		state.ApplyRuntimeStateChanges(rs)
		err := o.saveInternalState(ctx, state)
		if err != nil {
			return false, err
		}
	}
	log.Infof("Workflow actor '%s': workflow is stalled; holding reminder until context is canceled", o.actorID)
	return true, nil
}

// processPatchMismatch returns whether there is a patch mismatch and a description of the mismatch
func processPatchMismatch(historyPatches, currentPatches []string) (bool, string) {
	if len(historyPatches) == 0 {
		return false, ""
	}

	// History patches must be an exact prefix of current patches
	if len(currentPatches) >= len(historyPatches) &&
		slices.Equal(historyPatches, currentPatches[:len(historyPatches)]) {
		return false, ""
	}

	return true, fmt.Sprintf("Patch mismatch. History patches: [%s], current patches: [%s]. ",
		strings.Join(historyPatches, ", "),
		strings.Join(currentPatches, ", "))
}
