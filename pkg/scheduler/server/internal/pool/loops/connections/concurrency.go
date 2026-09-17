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

package connections

import (
	"strconv"
	"strings"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

const maxPendingPerGate = 100_000

// streamGatePrefix keys the sidecar slot gates declared through
// ConcurrencyLimitStream: "stream:<sidecar>:<actorType>", where sidecar is the
// stream's reported actor address, or "idx:<streamIDx>" when it reports none.
const streamGatePrefix = "stream:"

func pullGateKey(sidecar, actorType string) string {
	return streamGatePrefix + sidecar + ":" + actorType
}

// streamSidecarID identifies the sidecar behind a stream so that its several
// concurrent streams share one slot pool. Without an actor address every
// stream is its own sidecar.
func streamSidecarID(streamIDx uint64, actorAddress string) string {
	if actorAddress != "" {
		return actorAddress
	}
	return "idx:" + strconv.FormatUint(streamIDx, 10)
}

// streamGateKey is the gate key of a stream that reports no actor address.
func streamGateKey(streamIDx uint64, actorType string) string {
	return pullGateKey(streamSidecarID(streamIDx, ""), actorType)
}

// concurrencyGate enforces a local concurrency limit for a given gate key.
// Each scheduler enforces a deterministic share of globalLimit so that the
// cluster-wide sum equals globalLimit when globalLimit >= schedulerCount.
// A perStream gate belongs to exactly one stream, which is connected to
// exactly one scheduler, so its limit is used as-is.
type concurrencyGate struct {
	globalLimit uint32
	current     uint32
	perStream   bool
	pending     []*loops.TriggerRequest
}

func (g *concurrencyGate) free(schedulerCount, schedulerIdx uint32) uint32 {
	limit := g.globalLimit
	if !g.perStream {
		limit = localLimitFromGlobal(g.globalLimit, schedulerCount, schedulerIdx)
	}
	if g.current >= limit {
		return 0
	}
	return limit - g.current
}

// localLimitFromGlobal computes the local concurrency limit for a scheduler.
// Base slots are distributed evenly; the remainder is assigned to schedulers
// with the lowest indices so the cluster-wide sum equals globalLimit. When
// globalLimit < schedulerCount the share would be zero; we allow 1 everywhere
// to avoid starvation, at the cost of exceeding globalLimit.
func localLimitFromGlobal(globalLimit, schedulerCount, schedulerIdx uint32) uint32 {
	if schedulerCount <= 1 {
		return globalLimit
	}
	base := globalLimit / schedulerCount
	if base < 1 {
		return 1
	}
	if schedulerIdx < globalLimit%schedulerCount {
		return base + 1
	}
	return base
}

func (g *concurrencyGate) tryAcquire(schedulerCount, schedulerIdx uint32) bool {
	if g.free(schedulerCount, schedulerIdx) > 0 {
		g.current++
		return true
	}
	return false
}

func (g *concurrencyGate) release() {
	if g.current > 0 {
		g.current--
	}
}

func (g *concurrencyGate) enqueue(req *loops.TriggerRequest) bool {
	if len(g.pending) >= maxPendingPerGate {
		return false
	}
	g.pending = append(g.pending, req)
	return true
}

func (g *concurrencyGate) dequeue() *loops.TriggerRequest {
	if len(g.pending) == 0 {
		return nil
	}
	req := g.pending[0]
	g.pending[0] = nil
	g.pending = g.pending[1:]

	if len(g.pending) == 0 {
		g.pending = nil
	} else if cap(g.pending) > 64 && len(g.pending)*4 <= cap(g.pending) {
		pending := make([]*loops.TriggerRequest, len(g.pending))
		copy(pending, g.pending)
		g.pending = pending
	}

	return req
}

// requeueFront returns skipped requests to the front of the pending queue in
// their original order, preserving their seniority over later arrivals.
func (g *concurrencyGate) requeueFront(reqs []*loops.TriggerRequest) {
	if len(reqs) == 0 {
		return
	}
	g.pending = append(reqs, g.pending...)
}

func (g *concurrencyGate) pendingLen() int {
	return len(g.pending)
}

// pullQueue parks pull-dispatched triggers of one actor type while no stream
// has a free slot. Triggers are grouped by workflow instance and served round
// robin across instances, so one instance with a large fan-out cannot starve
// the others.
type pullQueue struct {
	byInstance map[string][]*loops.TriggerRequest
	ring       []string
	next       int
	n          int
}

func newPullQueue() *pullQueue {
	return &pullQueue{byInstance: make(map[string][]*loops.TriggerRequest)}
}

// pullInstanceKey returns the workflow instance ID encoded in an activity
// actor ID "<instanceID>::<taskID>::<generation>". The instance ID may itself
// contain the separator, so the last two components are cut. Any other actor
// ID is its own key.
func pullInstanceKey(actorID string) string {
	if i := strings.LastIndex(actorID, "::"); i >= 0 {
		if j := strings.LastIndex(actorID[:i], "::"); j >= 0 {
			return actorID[:j]
		}
	}
	return actorID
}

func (q *pullQueue) len() int {
	return q.n
}

func (q *pullQueue) enqueue(req *loops.TriggerRequest) bool {
	if q.n >= maxPendingPerGate {
		return false
	}
	key := pullInstanceKey(req.Job.GetMetadata().GetTarget().GetActor().GetId())
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
func (q *pullQueue) tryDispatchOne(fn func(*loops.TriggerRequest) bool) bool {
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
func (q *pullQueue) drainAll(fn func(*loops.TriggerRequest)) {
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
