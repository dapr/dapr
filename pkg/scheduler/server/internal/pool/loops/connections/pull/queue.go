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

package pull

import (
	"strings"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// queue parks pull-dispatched triggers of one actor type while no
// sidecar has a free slot. Triggers are grouped by workflow instance and
// served round robin across instances, so one instance with a large fan-out
// cannot starve the others.
type queue struct {
	max        int
	byInstance map[string][]*loops.TriggerRequest
	ring       []string
	next       int
	n          int
}

func newQueue(maxLen int) *queue {
	return &queue{max: maxLen, byInstance: make(map[string][]*loops.TriggerRequest)}
}

// instanceKey returns the workflow instance ID encoded in an activity
// actor ID "<instanceID>::<taskID>::<generation>". The instance ID may itself
// contain the separator, so the last two components are cut. Any other actor
// ID is its own key.
func instanceKey(actorID string) string {
	if i := strings.LastIndex(actorID, "::"); i >= 0 {
		if j := strings.LastIndex(actorID[:i], "::"); j >= 0 {
			return actorID[:j]
		}
	}
	return actorID
}

func (q *queue) len() int {
	return q.n
}

func (q *queue) enqueue(req *loops.TriggerRequest) bool {
	if q.n >= q.max {
		return false
	}
	key := instanceKey(req.Job.GetMetadata().GetTarget().GetActor().GetId())
	if _, ok := q.byInstance[key]; !ok {
		q.ring = append(q.ring, key)
	}
	q.byInstance[key] = append(q.byInstance[key], req)
	q.n++
	return true
}

// tryDispatchOne offers the head trigger of each instance, in ring order
// starting after the last served instance, to fn until one is accepted. The
// accepted trigger is removed and the rotation advances past its instance.
// Returns false when no head was accepted in a full rotation.
func (q *queue) tryDispatchOne(fn func(*loops.TriggerRequest) bool) bool {
	for i := range len(q.ring) {
		pos := (q.next + i) % len(q.ring)
		key := q.ring[pos]
		reqs := q.byInstance[key]
		if !fn(reqs[0]) {
			continue
		}

		reqs[0] = nil
		if len(reqs) == 1 {
			delete(q.byInstance, key)
			q.ring = append(q.ring[:pos], q.ring[pos+1:]...)
			if len(q.ring) == 0 {
				q.next = 0
			} else {
				q.next = pos % len(q.ring)
			}
		} else {
			q.byInstance[key] = reqs[1:]
			q.next = (pos + 1) % len(q.ring)
		}
		q.n--
		return true
	}
	return false
}

// drainAll removes every parked trigger, passing each to fn.
func (q *queue) drainAll(fn func(*loops.TriggerRequest)) {
	for _, key := range q.ring {
		for _, req := range q.byInstance[key] {
			fn(req)
		}
	}
	q.byInstance = make(map[string][]*loops.TriggerRequest)
	q.ring = nil
	q.next = 0
	q.n = 0
}
