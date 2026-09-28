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

import "sync"

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
	m  map[int32]string
}

// arm records that taskID was dispatched under execID and its result is owed.
func (a *awaitedResults) arm(taskID int32, execID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m == nil {
		a.m = make(map[int32]string, 1)
	}
	a.m[taskID] = execID
}

// settle records that taskID's result arrived, whatever was then done with
// it: admitted, or acknowledged and dropped. Nothing is owed for a result
// that has been judged. A result naming another execution of the task
// settles nothing, since the dispatch this entry stands for is still out.
//
// An empty execution id on either side matches: dapr authors synthetic
// TaskFailed events without one, and SDKs older than execution ids send
// results without one.
func (a *awaitedResults) settle(taskID int32, execID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	armed, ok := a.m[taskID]
	if !ok {
		return
	}
	if armed == "" || execID == "" || armed == execID {
		delete(a.m, taskID)
	}
}

// any reports whether any dispatched result is still owed.
func (a *awaitedResults) any() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.m) > 0
}
