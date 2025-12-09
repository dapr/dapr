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
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/ptr"
)

func isStalled(ctx context.Context, o *orchestrator, state *wfenginestate.State, rs *backend.OrchestrationRuntimeState) bool {
	historyPatches := getLastPatches(rs.OldEvents)
	currentPatches := getLastPatches(rs.NewEvents)
	hasMismatch, description := processPatchMismatch(historyPatches, currentPatches)
	if !hasMismatch {
		return false
	}

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
				Reason:      protos.StalledReason_PATCH_MISMATCH,
				Description: ptr.Of(description),
			},
		},
	})

	state.ApplyRuntimeStateChanges(rs)
	err := o.saveInternalState(ctx, state)
	if err != nil {
		log.Errorf("Workflow actor '%s': failed to save internal state: %v", o.actorID, err)
		return false
	}
	log.Infof("Workflow actor '%s': workflow is stalled; holding reminder until context is canceled", o.actorID)
	return true
}

func getLastPatches(events []*protos.HistoryEvent) []string {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if os := e.GetOrchestratorStarted(); os != nil {
			if version := os.GetVersion(); version != nil {
				return version.GetPatches()
			}
		}
	}
	return nil
}

// processPatchMismatch returns whether there is a patch mismatch and a description of the mismatch
func processPatchMismatch(historyPatches, currentPatches []string) (bool, string) {
	if len(historyPatches) == 0 {
		return false, ""
	}

	// Build sets for easier comparison
	historySet := make(map[string]struct{}, len(historyPatches))
	for _, p := range historyPatches {
		historySet[p] = struct{}{}
	}
	currentSet := make(map[string]struct{}, len(currentPatches))
	for _, p := range currentPatches {
		currentSet[p] = struct{}{}
	}

	// Find missing patches (in history but not in current)
	var missingPatches []string
	for _, p := range historyPatches {
		if _, ok := currentSet[p]; !ok {
			missingPatches = append(missingPatches, p)
		}
	}

	// Find extra patches (in current but not in history)
	var extraPatches []string
	for _, p := range currentPatches {
		if _, ok := historySet[p]; !ok {
			extraPatches = append(extraPatches, p)
		}
	}

	// Check for order mismatch (same patches but different order)
	orderMismatch := false
	if len(missingPatches) == 0 && len(extraPatches) == 0 && len(historyPatches) == len(currentPatches) {
		for i := range historyPatches {
			if historyPatches[i] != currentPatches[i] {
				orderMismatch = true
				break
			}
		}
	}

	// No mismatch
	if len(missingPatches) == 0 && len(extraPatches) == 0 && !orderMismatch {
		return false, ""
	}

	// Build description
	var parts []string
	if len(missingPatches) > 0 {
		parts = append(parts, fmt.Sprintf("missing patches: [%s]", strings.Join(missingPatches, ", ")))
	}
	if len(extraPatches) > 0 {
		parts = append(parts, fmt.Sprintf("unexpected patches: [%s]", strings.Join(extraPatches, ", ")))
	}
	if orderMismatch {
		parts = append(parts, fmt.Sprintf("patch order mismatch: history has [%s], current has [%s]",
			strings.Join(historyPatches, ", "), strings.Join(currentPatches, ", ")))
	}

	description := fmt.Sprintf("Patch mismatch - %s. The workflow was previously executed with patches [%s] but the current code has patches [%s]. "+
		"Deploy the correct code version or use workflow versioning to handle this transition.",
		strings.Join(parts, "; "),
		strings.Join(historyPatches, ", "),
		strings.Join(currentPatches, ", "))

	return true, description
}
