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
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/ptr"
)

func (o *orchestrator) handleStalled(ctx context.Context, state *wfenginestate.State, rs *backend.OrchestrationRuntimeState) error {
	for _, msg := range rs.GetPendingMessages() {
		if executionStalledEvent := msg.GetHistoryEvent().GetExecutionStalled(); executionStalledEvent != nil {
			return o.stallWorkflow(ctx, state, rs, executionStalledEvent.GetReason(), executionStalledEvent.GetDescription())
		}
	}

	patchMismatch, patchMismatchDescription := o.hasPatchMismatch(rs)
	if patchMismatch {
		return o.stallWorkflow(ctx, state, rs, protos.StalledReason_PATCH_MISMATCH, patchMismatchDescription)
	}
	return nil
}

func (o *orchestrator) hasPatchMismatch(rs *backend.OrchestrationRuntimeState) (bool, string) {
	historyPatches := collectAllPatches(rs.OldEvents)
	currentPatches := getLastPatches(rs.NewEvents)
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

func (o *orchestrator) stallWorkflow(ctx context.Context, state *wfenginestate.State, rs *backend.OrchestrationRuntimeState, reason protos.StalledReason, description string) error {
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
					Reason:      reason,
					Description: ptr.Of(description),
				},
			},
		})
	}
	if hasFilteredNewEvents || hasStalledEvent {
		state.ApplyRuntimeStateChanges(rs)
		err := o.saveInternalState(ctx, state)
		if err != nil {
			return err
		}
	}
	log.Infof("Workflow actor '%s': workflow is stalled; holding reminder until context is canceled", o.actorID)

	unlock := o.lock.Stall()
	defer unlock()
	<-ctx.Done()

	return errors.New("workflow is stalled")
}

func collectAllPatches(events []*protos.HistoryEvent) []string {
	var allPatches []string
	for _, e := range events {
		if os := e.GetOrchestratorStarted(); os != nil {
			if v := os.GetVersion(); v != nil {
				allPatches = append(allPatches, v.GetPatches()...)
			}
		}
	}
	return allPatches
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

func compactPatches(rs *backend.OrchestrationRuntimeState) {
	for _, e := range rs.NewEvents {
		if os := e.GetOrchestratorStarted(); os != nil {
			if v := os.GetVersion(); v != nil && len(v.GetPatches()) > 0 {
				existingPatchCounts := make(map[string]int)
				for _, oldEvent := range rs.OldEvents {
					if oldOS := oldEvent.GetOrchestratorStarted(); oldOS != nil {
						if oldV := oldOS.GetVersion(); oldV != nil {
							for _, p := range oldV.GetPatches() {
								existingPatchCounts[p]++
							}
						}
					}
				}

				var newPatches []string
				for _, p := range v.GetPatches() {
					if existingPatchCounts[p] > 0 {
						existingPatchCounts[p]--
					} else {
						newPatches = append(newPatches, p)
					}
				}

				if len(newPatches) > 0 {
					os.Version = &protos.OrchestrationVersion{Patches: newPatches}
				} else {
					os.Version = nil
				}
			}
			break
		}
	}
}
