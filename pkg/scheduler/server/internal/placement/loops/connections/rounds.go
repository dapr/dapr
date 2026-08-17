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
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops"
)

// markPending marks the given actor types as having table changes which are
// not yet covered by a dissemination round.
func (c *connections) markPending(types []string) {
	for _, t := range types {
		c.pendingTypes[t] = struct{}{}
	}
}

// maybeDisseminate starts a dissemination round covering every pending actor
// type which is not locked by an in-flight round. Types locked by in-flight
// rounds stay pending and are picked up when their round completes. While a
// coalesce timer is armed, changes accumulate until it fires.
func (c *connections) maybeDisseminate() {
	if len(c.pendingTypes) == 0 || c.coalesceTimer != nil {
		return
	}

	if len(c.streams) == 0 {
		clear(c.pendingTypes)
		return
	}

	types := make([]string, 0, len(c.pendingTypes))
	for t := range c.pendingTypes {
		if _, locked := c.lockedTypes[t]; locked {
			continue
		}
		types = append(types, t)
	}

	if len(types) == 0 {
		return
	}
	sort.Strings(types)

	seq := c.nextSeq()
	r := &round{
		seq:     seq,
		types:   types,
		phase:   schedulerv1pb.Operation_OPERATION_LOCK,
		members: make(map[uint64]struct{}, len(c.streams)),
		acked:   make(map[uint64]struct{}, len(c.streams)),
		started: time.Now(),
	}

	monitoring.RecordPlacementDissemination(c.namespace)
	for _, t := range types {
		c.versions[t]++
		c.lockedTypes[t] = seq
		delete(c.pendingTypes, t)
		monitoring.RecordPlacementTableUpdate(c.namespace, t)
	}

	c.rounds[seq] = r
	c.timeoutQ.Enqueue(seq)

	log.Debugf("Starting dissemination round seq=%d types=%v in %s", seq, types, c.namespace)

	for idx, conn := range c.streams {
		r.members[idx] = struct{}{}
		conn.loop.Enqueue(&loops.SendLock{Seq: seq, Types: types})
	}
}

// handleAck processes a placement order ack from a stream.
func (c *connections) handleAck(ack *loops.Ack) {
	if _, ok := c.streams[ack.StreamIDx]; !ok {
		return
	}

	// Acks for one-shot snapshot pushes are not part of any round.
	if idx, ok := c.oneshots[ack.Seq]; ok {
		if idx == ack.StreamIDx && ack.Operation == schedulerv1pb.Operation_OPERATION_UNLOCK {
			delete(c.oneshots, ack.Seq)
		}
		return
	}

	r, ok := c.rounds[ack.Seq]
	if !ok {
		return
	}

	if _, member := r.members[ack.StreamIDx]; !member {
		return
	}

	if ack.Operation != r.phase {
		return
	}

	r.acked[ack.StreamIDx] = struct{}{}
	c.advanceRounds([]uint64{ack.Seq})
}

// advanceRounds advances every listed round whose current phase has been
// acked by all members.
func (c *connections) advanceRounds(seqs []uint64) {
	for _, seq := range seqs {
		r, ok := c.rounds[seq]
		if !ok {
			continue
		}

		if len(r.members) == 0 {
			// Every participant is gone; nothing left to send. The types
			// were re-marked pending when the streams were removed if any
			// hosts remained.
			c.completeRound(r)
			continue
		}

		if len(r.acked) < len(r.members) {
			continue
		}

		switch r.phase {
		case schedulerv1pb.Operation_OPERATION_LOCK:
			r.phase = schedulerv1pb.Operation_OPERATION_UPDATE
			clear(r.acked)

			// Tables are built at send time so the UPDATE always carries the
			// latest membership, even when it changed after the round began.
			tables := c.store.Tables(r.types)
			versions := make(map[string]uint64, len(r.types))
			for _, t := range r.types {
				versions[t] = c.versions[t]
			}

			for idx := range r.members {
				c.streams[idx].loop.Enqueue(&loops.SendUpdate{
					Seq:      r.seq,
					Types:    r.types,
					Versions: versions,
					Tables:   tables,
				})
			}

		case schedulerv1pb.Operation_OPERATION_UPDATE:
			r.phase = schedulerv1pb.Operation_OPERATION_UNLOCK
			clear(r.acked)

			for idx := range r.members {
				c.streams[idx].loop.Enqueue(&loops.SendUnlock{Seq: r.seq, Types: r.types})
			}

		case schedulerv1pb.Operation_OPERATION_UNLOCK:
			log.Debugf("Dissemination round seq=%d types=%v in %s complete", r.seq, r.types, c.namespace)
			c.completeRound(r)
		}
	}
}

// completeRound finishes a round, releasing its types. When a coalesce window
// is configured the timer is armed so churn which arrived during the round is
// batched; otherwise any pending types disseminate immediately.
func (c *connections) completeRound(r *round) {
	if !r.started.IsZero() {
		monitoring.RecordPlacementDisseminationLatency(c.namespace, time.Since(r.started))
	}
	c.timeoutQ.Dequeue(r.seq)
	delete(c.rounds, r.seq)
	for _, t := range r.types {
		if c.lockedTypes[t] == r.seq {
			delete(c.lockedTypes, t)
		}
	}

	if c.coalesceWindow > 0 && len(c.pendingTypes) > 0 {
		c.startCoalesceTimer()
		return
	}

	c.maybeDisseminate()
}

// handleTimeout fires when a round has not completed within the disseminate
// timeout. Members which failed to ack the current phase are evicted; the
// round is aborted and its types re-disseminated to the survivors.
func (c *connections) handleTimeout(t *loops.RoundTimeout) {
	r, ok := c.rounds[t.Seq]
	if !ok {
		return
	}

	err := status.Errorf(
		codes.DeadlineExceeded,
		"dissemination timeout after %s for round %d",
		c.disseminateTimeout,
		t.Seq,
	)

	log.Warnf("Dissemination timeout for round seq=%d types=%v in %s", t.Seq, r.types, c.namespace)

	var affected []uint64
	for idx := range r.members {
		if _, acked := r.acked[idx]; acked {
			continue
		}

		log.Warnf("Closing non-responding placement stream %s:%d (phase=%s)", c.namespace, idx, r.phase)
		affected = append(affected, c.removeStream(idx, err)...)
	}

	// Abort the round: release and re-mark its types so a fresh round covers
	// them for the surviving streams.
	delete(c.rounds, t.Seq)
	for _, typ := range r.types {
		if c.lockedTypes[typ] == t.Seq {
			delete(c.lockedTypes, typ)
		}
	}

	// Survivors still hold the aborted round's LOCK and timer: send its
	// UNLOCK so the round closes cleanly, or their timers reset the streams.
	for idx := range r.members {
		conn, ok := c.streams[idx]
		if !ok {
			continue
		}
		conn.loop.Enqueue(&loops.SendUnlock{Seq: t.Seq, Types: r.types})
	}

	if len(c.streams) > 0 {
		c.markPending(r.types)
	}

	c.advanceRounds(affected)
	c.maybeDisseminate()
}

// startCoalesceTimer arms a one-shot timer that enqueues CoalesceFire after
// the configured coalesce window. Idempotent.
func (c *connections) startCoalesceTimer() {
	if c.coalesceWindow <= 0 || c.coalesceTimer != nil {
		return
	}
	c.coalesceTimer = time.AfterFunc(c.coalesceWindow, func() {
		c.loop.Enqueue(&loops.CoalesceFire{})
	})
}

// stopCoalesceTimer cancels any pending coalesce timer.
func (c *connections) stopCoalesceTimer() {
	if c.coalesceTimer != nil {
		c.coalesceTimer.Stop()
		c.coalesceTimer = nil
	}
}

// handleCoalesceFire drains the coalesce window into a dissemination round.
func (c *connections) handleCoalesceFire() {
	c.coalesceTimer = nil
	c.maybeDisseminate()
}
