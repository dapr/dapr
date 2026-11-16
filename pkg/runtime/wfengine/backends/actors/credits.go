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
	"sync"
)

// notifier hands out a channel that is closed on the next Notify, so a reader
// can wait for a change and then read the latest value.
type notifier struct {
	lock sync.Mutex
	ch   chan struct{}
}

func (n *notifier) wait() <-chan struct{} {
	n.lock.Lock()
	defer n.lock.Unlock()
	if n.ch == nil {
		n.ch = make(chan struct{})
	}
	return n.ch
}

func (n *notifier) notify() {
	n.lock.Lock()
	defer n.lock.Unlock()
	if n.ch != nil {
		close(n.ch)
		n.ch = nil
	}
}

// reserveActivity counts an activity work item handed to the engine. A pull
// delivery is refused when the sidecar is at its activity cap; any other
// delivery is counted and then queues for the engine as before.
func (abe *Actors) reserveActivity(pull bool) bool {
	for {
		c := abe.activityCommitted.Load()
		if pull && abe.activityCap > 0 && c >= abe.activityCap {
			return false
		}
		if abe.activityCommitted.CompareAndSwap(c, c+1) {
			abe.activityFree.notify()
			return true
		}
	}
}

func (abe *Actors) releaseActivity() {
	abe.activityCommitted.Add(-1)
	abe.activityFree.notify()
}

// ActivityFreeSlots returns the number of activity work items this sidecar can
// take before reaching its activity cap, counting every activity it holds
// whatever the dispatch mode, and a channel closed on the next change. The
// channel is taken before the count so no change is missed.
func (abe *Actors) ActivityFreeSlots() (uint32, <-chan struct{}) {
	changed := abe.activityFree.wait()
	//nolint:gosec // bounded by activityCap, an int32
	return uint32(max(0, abe.activityCap-abe.activityCommitted.Load())), changed
}
