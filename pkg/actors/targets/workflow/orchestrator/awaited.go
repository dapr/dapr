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
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
)

// awaitedResults records the activity results this actor has dispatched and
// not yet seen come back. A completed instance refuses reuse of its ID while
// any is outstanding, so that a fresh generation cannot start under an ID
// whose previous activity is still running and about to report.
//
// Keyed by task id rather than by the (task id, execution id) pair: a new
// generation re-dispatching the same task id replaces the entry, which is
// what lets a terminated instance's ID be reused once the current
// scheduling's result has arrived, even though a straggler of the previous
// scheduling may still be in flight. Keying by the pair would hold the ID
// for that straggler too.
type awaitedResults struct {
	mu sync.Mutex
	m  map[int32]awaitedEntry
}

type awaitedEntry struct {
	execID string
	// refusedAt is when this dispatch's result was first refused
	// recoverably, or zero while it is still expected to be admitted.
	refusedAt time.Time
}

// arm records that taskID was dispatched under execID and its result is owed.
func (a *awaitedResults) arm(taskID int32, execID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m == nil {
		a.m = make(map[int32]awaitedEntry, 1)
	}
	// A re-dispatch owes the result afresh, whatever happened to the last.
	a.m[taskID] = awaitedEntry{execID: execID}
}

// matches reports whether execID names the dispatch armed for taskID. An
// empty execution id on either side matches: dapr authors synthetic
// TaskFailed events without one, and SDKs older than execution ids send
// results without one.
func (a *awaitedResults) matches(armed awaitedEntry, execID string) bool {
	return armed.execID == "" || execID == "" || armed.execID == execID
}

// settle records that taskID's result arrived and was judged, whatever was
// then done with it: admitted, or acknowledged and dropped. Nothing is owed
// for a result that has been judged.
func (a *awaitedResults) settle(taskID int32, execID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if armed, ok := a.m[taskID]; ok && a.matches(armed, execID) {
		delete(a.m, taskID)
	}
}

// refuse records that taskID's result was refused recoverably. The sender
// keeps it in hand and retries for the publish window, then drops it for
// good with nothing left to re-deliver it, so the dispatch stops being owed
// once that window has passed. The verdict alone cannot release it: a
// refusal is routinely cleared by a later retry once a lagging read catches
// up, and that retry must still find the ID held.
func (a *awaitedResults) refuse(taskID int32, execID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	armed, ok := a.m[taskID]
	if !ok || !a.matches(armed, execID) || !armed.refusedAt.IsZero() {
		return
	}
	armed.refusedAt = time.Now()
	a.m[taskID] = armed
}

// any reports whether any dispatched result is still owed.
func (a *awaitedResults) any() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.m {
		if e.refusedAt.IsZero() || time.Since(e.refusedAt) < common.PublishRetryWindow() {
			return true
		}
	}
	return false
}
