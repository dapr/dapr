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

package actors

import (
	"strconv"
	"sync"
)

// activityExecutions tracks, per (workflow instance, task id), how many
// completion registrations the durabletask engine currently holds on this
// host. A registration exists from the moment the worker dispatches an
// activity work item until its completion, cancellation, or deregistration
// is delivered, so a held count > 0 means the work item is live: dispatched
// or awaiting the app. The activity target's stale-claim eviction consults
// it to tell a live long-running execution (never evicted) from one whose
// work item was lost without ever resolving (the janitor-livelock class).
type activityExecutions struct {
	lock      sync.Mutex
	held      map[string]int
	resolvers map[string]*func()
}

func newActivityExecutions() *activityExecutions {
	return &activityExecutions{held: make(map[string]int), resolvers: make(map[string]*func())}
}

func activityExecutionKey(instanceID string, taskID int32) string {
	return instanceID + "::" + strconv.FormatInt(int64(taskID), 10)
}

// add records a registration and returns its idempotent release.
func (a *activityExecutions) add(key string) func() {
	a.lock.Lock()
	a.held[key]++
	a.lock.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			a.lock.Lock()
			if n := a.held[key]; n <= 1 {
				delete(a.held, key)
			} else {
				a.held[key] = n - 1
			}
			a.lock.Unlock()
		})
	}
}

func (a *activityExecutions) heldFor(instanceID string, taskID int32) bool {
	a.lock.Lock()
	defer a.lock.Unlock()
	return a.held[activityExecutionKey(instanceID, taskID)] > 0
}

func (a *activityExecutions) registerResolver(instanceID string, taskID int32, resolve func()) func() {
	key := activityExecutionKey(instanceID, taskID)
	entry := &resolve
	a.lock.Lock()
	a.resolvers[key] = entry
	a.lock.Unlock()
	return func() {
		a.lock.Lock()
		if a.resolvers[key] == entry {
			delete(a.resolvers, key)
		}
		a.lock.Unlock()
	}
}

func (a *activityExecutions) resolve(key string) {
	a.lock.Lock()
	entry := a.resolvers[key]
	a.lock.Unlock()
	if entry != nil {
		(*entry)()
	}
}
