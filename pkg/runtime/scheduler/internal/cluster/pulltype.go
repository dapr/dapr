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

package cluster

import (
	"maps"
	"slices"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// pullType is an actor type this sidecar pull-dispatches.
type pullType struct {
	// free is unused when workflow is set. Its channel is taken before the count
	// so no change is missed.
	free     func() (uint32, <-chan struct{})
	workflow *workflowSlots
}

// local reports whether a delivery runs on this host. A workflow turn goes to
// the placement owner, which is the only host that can run it.
func (pt pullType) local() bool {
	return pt.workflow == nil
}

func (pt pullType) slots() (uint32, <-chan struct{}) {
	if pt.workflow != nil {
		return pt.workflow.free()
	}
	return pt.free()
}

func sortedTypes(types map[string]pullType) []string {
	return slices.Sorted(maps.Keys(types))
}

// pullSlots returns the free slots of each type, sorted by actor type.
func pullSlots(types map[string]pullType) []*schedulerv1pb.PullSlots {
	slots := make([]*schedulerv1pb.PullSlots, 0, len(types))
	for _, actorType := range sortedTypes(types) {
		free, _ := types[actorType].slots()
		slots = append(slots, &schedulerv1pb.PullSlots{ActorType: actorType, Free: free})
	}
	return slots
}

// workflowSlots counts held workflow turns against maxConcurrentWorkflowInvocations.
// Turns of one instance run one at a time on its owner, so they share a slot.
type workflowSlots struct {
	lock    sync.Mutex
	capa    int64
	held    map[string]int
	changed chan struct{}
}

func newWorkflowSlots(capa int32) *workflowSlots {
	return &workflowSlots{capa: int64(capa), held: make(map[string]int), changed: make(chan struct{})}
}

func (t *workflowSlots) reserve(key string) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.held[key] == 0 && int64(len(t.held)) >= t.capa {
		return false
	}
	t.held[key]++
	t.signalLocked()
	return true
}

func (t *workflowSlots) release(key string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.held[key]--; t.held[key] <= 0 {
		delete(t.held, key)
	}
	t.signalLocked()
}

func (t *workflowSlots) free() (uint32, <-chan struct{}) {
	t.lock.Lock()
	defer t.lock.Unlock()
	//nolint:gosec // bounded by capa, an int32
	return uint32(max(0, t.capa-int64(len(t.held)))), t.changed
}

func (t *workflowSlots) signalLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}
