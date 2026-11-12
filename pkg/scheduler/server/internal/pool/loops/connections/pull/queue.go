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
	"slices"
	"strings"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// queue parks pull-dispatched triggers of one actor type while no
// sidecar has a free slot. Triggers are grouped by workflow instance and
// activity name and served round robin across groups, so one instance with a
// large fan-out cannot starve the others, and a full per-name gate does not
// hold back the instance's other activities.
type queue struct {
	max    int
	groups map[string][]*loops.TriggerRequest
	ring   []string
	next   int
	n      int
}

func newQueue(maxLen int) *queue {
	return &queue{max: maxLen, groups: make(map[string][]*loops.TriggerRequest)}
}

// groupKey is the workflow instance and activity name of a parked trigger.
func groupKey(req *loops.TriggerRequest) string {
	meta := req.Job.GetMetadata()
	return instanceKey(meta.GetTarget().GetActor().GetId()) + "||" + meta.GetConcurrencyKey()
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

// enqueue parks req, refusing it at the bound unless admitted: a trigger a
// sidecar already accepted and refused is parked past the bound, so a refusal
// never reaches its failure policy.
func (q *queue) enqueue(req *loops.TriggerRequest, admitted bool) bool {
	if !admitted && q.n >= q.max {
		return false
	}
	key := groupKey(req)
	if _, ok := q.groups[key]; !ok {
		q.ring = append(q.ring, key)
	}
	q.groups[key] = append(q.groups[key], req)
	q.n++
	return true
}

// tryDispatchOne offers the head trigger of each group, in ring order
// starting after the last served group, to fn until one is accepted. The
// accepted trigger is removed and the rotation advances past its group.
// Returns false when no head was accepted in a full rotation.
func (q *queue) tryDispatchOne(fn func(*loops.TriggerRequest) bool) bool {
	for i := range len(q.ring) {
		pos := (q.next + i) % len(q.ring)
		key := q.ring[pos]
		reqs := q.groups[key]
		if !fn(reqs[0]) {
			continue
		}

		reqs[0] = nil
		if len(reqs) == 1 {
			delete(q.groups, key)
			q.ring = append(q.ring[:pos], q.ring[pos+1:]...)
			if len(q.ring) == 0 {
				q.next = 0
			} else {
				q.next = pos % len(q.ring)
			}
		} else {
			q.groups[key] = reqs[1:]
			q.next = (pos + 1) % len(q.ring)
		}
		q.n--
		return true
	}
	return false
}

// remove takes the parked trigger of the job stored under key out of the
// queue, or returns nil.
func (q *queue) remove(key string) *loops.TriggerRequest {
	for pos, group := range q.ring {
		reqs := q.groups[group]
		i := slices.IndexFunc(reqs, func(req *loops.TriggerRequest) bool { return req.Job.GetKey() == key })
		if i < 0 {
			continue
		}
		req := reqs[i]
		if len(reqs) > 1 {
			q.groups[group] = slices.Delete(reqs, i, i+1)
		} else {
			delete(q.groups, group)
			q.ring = slices.Delete(q.ring, pos, pos+1)
			if q.next > pos {
				q.next--
			}
			if q.next >= len(q.ring) {
				q.next = 0
			}
		}
		q.n--
		return req
	}
	return nil
}

// drainAll removes every parked trigger, passing each to fn.
func (q *queue) drainAll(fn func(*loops.TriggerRequest)) {
	for _, key := range q.ring {
		for _, req := range q.groups[key] {
			fn(req)
		}
	}
	q.groups = make(map[string][]*loops.TriggerRequest)
	q.ring = nil
	q.next = 0
	q.n = 0
}
