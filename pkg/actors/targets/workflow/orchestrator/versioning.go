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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

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
