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

package disseminator

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

func (d *disseminator) handleLookupRequest(req *loops.LookupRequest) {
	d.inflight.AcquireLookup(req)
}

func (d *disseminator) handleAcquireRequest(req *loops.LockRequest) {
	// A state call of a type which is blocked only because its actors are
	// being halted in place is not queued. It may belong to an in-flight
	// call, which the halt waits for: queued, that call could not complete
	// before the drain timeout.
	if d.halting[req.ActorType] > 0 && !d.roundBlocks(req.ActorType) {
		d.inflight.AcquireUnqueued(req)
		return
	}
	d.inflight.Acquire(req)
}

// handleHaltActorTypes halts the actors of the given types the way a
// dissemination round halts the actors it moves: new calls of those types
// queue, in-flight calls drain with the configured timeout, then the actors
// are halted. Unlike a round, the drain does not hold this loop, so the other
// actor types are served meanwhile, and so are the state calls of the halted
// types, which lets an in-flight call save its state and complete. Types a
// round blocks stay blocked when the halt completes.
func (d *disseminator) handleHaltActorTypes(e *loops.HaltActorTypes) {
	d.inflight.LockTypes(e.ActorTypes)
	for _, actorType := range e.ActorTypes {
		d.halting[actorType]++
	}
	d.pendingHalts[e] = struct{}{}

	drained := d.inflight.DrainClaimsForTypes(e.ActorTypes, errors.New("bound actor state store changed"))
	// d is pooled once this loop shuts down, so the goroutine does not use it.
	lp, closeCh := d.loop, d.closeCh
	d.wg.Go(func() {
		select {
		case <-drained:
			lp.Enqueue(&loops.HaltActorTypesDrained{Halt: e})
		case <-closeCh:
		}
	})
}

func (d *disseminator) handleHaltActorTypesDrained(ctx context.Context, e *loops.HaltActorTypesDrained) {
	if _, ok := d.pendingHalts[e.Halt]; ok {
		d.completeHalt(ctx, e.Halt)
	}
}

// completeHalt halts the actors of a drained halt and unblocks its types,
// except those another halt or a round still blocks.
func (d *disseminator) completeHalt(ctx context.Context, e *loops.HaltActorTypes) {
	delete(d.pendingHalts, e)

	e.Done <- e.Halt(ctx)

	unlock := make([]string, 0, len(e.ActorTypes))
	for _, actorType := range e.ActorTypes {
		d.halting[actorType]--
		if d.halting[actorType] > 0 {
			continue
		}
		delete(d.halting, actorType)
		if !d.roundBlocks(actorType) {
			unlock = append(unlock, actorType)
		}
	}
	d.inflight.UnlockTypes(unlock)
}

// completePendingHalts runs, on shutdown, the halts still waiting for their
// drain: closing inflight ended every claim, and this loop takes no more
// events.
func (d *disseminator) completePendingHalts(ctx context.Context) {
	close(d.closeCh)
	for e := range d.pendingHalts {
		d.completeHalt(ctx, e)
	}
}

// roundBlocks returns whether a dissemination round in progress blocks the
// actor type.
func (d *disseminator) roundBlocks(actorType string) bool {
	if _, ok := d.roundChangedTypes[actorType]; ok {
		return true
	}
	for _, r := range d.v2Rounds {
		if _, ok := r.changed[actorType]; ok || slices.Contains(r.scope, actorType) {
			return true
		}
	}
	return false
}

// unlockRoundTypes unblocks the types a completed round blocked. A type
// whose actors are being halted stays blocked until the halt completes,
// unless another round blocks it, but its state calls which queued during
// the round are served, as handleAcquireRequest serves them from now on.
func (d *disseminator) unlockRoundTypes(actorTypes []string) {
	var halting []string
	actorTypes = slices.DeleteFunc(actorTypes, func(actorType string) bool {
		if d.halting[actorType] == 0 {
			return false
		}
		if !d.roundBlocks(actorType) {
			halting = append(halting, actorType)
		}
		return true
	})
	d.inflight.UnlockTypes(actorTypes)
	d.inflight.FlushLocks(halting)
}

// completeRound runs at the end of every dissemination round on this
// stream, once its tables are installed.
func (d *disseminator) completeRound(ctx context.Context) {
	d.roundCompleted = true
	// A type in a round still in progress keeps draining: its installed
	// table is not final until that round completes.
	pendingAll, pending := d.pendingTypes()
	d.actorTable.CompleteDrains(ctx, func(actorType string) bool {
		_, ok := pending[actorType]
		return pendingAll || ok || d.inflight.HasLocalHost(actorType)
	})
	d.placementLoop.Enqueue(&loops.RoundCompleted{
		Remote:     d.inflight.RemoteHostTypes(),
		Pending:    pending,
		PendingAll: pendingAll,
	})
	d.reloadReminderTypes()
}

// reloadReminderTypes advertises the actor types this host takes reminders
// for to the scheduler.
func (d *disseminator) reloadReminderTypes() {
	d.scheduler.ReloadActorTypes(d.actorTable.ReminderTypes())
}

// pendingTypes returns the actor types whose tables a dissemination round
// still in progress may change, or all if such a round is the startup
// snapshot: a v2 round with an empty scope covers every type.
func (d *disseminator) pendingTypes() (bool, map[string]struct{}) {
	pending := make(map[string]struct{}, len(d.roundChangedTypes))
	for actorType := range d.roundChangedTypes {
		pending[actorType] = struct{}{}
	}
	for _, r := range d.v2Rounds {
		if len(r.scope) == 0 {
			return true, nil
		}
		for _, actorType := range r.scope {
			pending[actorType] = struct{}{}
		}
		for actorType := range r.changed {
			pending[actorType] = struct{}{}
		}
	}
	return false, pending
}

func (d *disseminator) handleReportHost(report *loops.ReportHost) {
	d.streamLoop.Enqueue(&loops.StreamSend{
		Report: report.Report,
	})
}

func (d *disseminator) handleOrder(ctx context.Context, order *loops.StreamOrder) error {
	diag.DefaultMonitoring.ActorPlacementTableOperationReceived(order.Order.Op.String())

	version := order.Order.Version

	log.Debugf("Handling placement order=%s version=%d", order.Order.Op, version)

	switch order.Order.Op {
	case loops.OrderLock:
		// LOCK signals that the placement server is about to push a new
		// table. The new tables haven't arrived yet, so we don't know
		// which actor types will change. Lookups continue to resolve
		// against the current table; per-type queueing only kicks in at
		// UPDATE once the diff is known.
		d.timeoutQ.Dequeue(d.timeoutVersion)
		d.timeoutVersion++
		d.timeoutQ.Enqueue(d.timeoutVersion)

		d.currentOperation = loops.OrderLock
		d.currentVersion = version

		d.streamLoop.Enqueue(&loops.StreamSend{
			Ack: &loops.Ack{
				Op:      loops.OrderLock,
				Version: d.currentVersion,
			},
		})

	case loops.OrderUpdate:
		if d.currentVersion > version {
			d.streamLoop.Close(&loops.Shutdown{
				Error: fmt.Errorf("version mismatch: expected %d, got %d",
					d.currentVersion,
					version,
				),
			})
			return nil
		}

		d.timeoutQ.Dequeue(d.timeoutVersion)

		// Diff old vs new tables, install new tables, and block only the
		// actor types whose hash ring actually changed. Open ensures the
		// claim-tracking loop is running and flushes any queued requests
		// for unchanged types. Accumulate into roundChangedTypes so a
		// later UNLOCK releases every type touched across compressed
		// rounds (the placement server may elide intermediate UNLOCKs).
		changed := d.inflight.Set(order.Order.V1Tables, version)
		for _, t := range changed {
			d.roundChangedTypes[t] = struct{}{}
		}
		d.inflight.LockTypes(changed)
		d.inflight.Open(ctx)

		d.currentOperation = loops.OrderUpdate

		// Drain in-flight claims for actor types whose hash ring changed
		// in this UPDATE so the request layer can retry against the new
		// routing. Claims for unchanged types survive: a routine
		// dissemination round is no longer fatal to in-flight invocations
		// of unaffected types.
		d.inflight.CancelClaimsForTypes(changed, errors.New("placement table updated"))

		if err := d.actorTable.HaltNonHosted(ctx, d.inflight.IsActorHostedNoLock); err != nil {
			log.Errorf("Error draining non-hosted actors: %s", err)
		}

		d.streamLoop.Enqueue(&loops.StreamSend{
			Ack: &loops.Ack{
				Op:      loops.OrderUpdate,
				Version: d.currentVersion,
			},
		})

	case loops.OrderUnlock:
		if d.currentOperation != loops.OrderUpdate {
			log.Warnf("Invalid operation sequence: expected UPDATE before UNLOCK, ignoring unlock")
			return nil
		}

		if d.currentVersion > version {
			log.Errorf("Version mismatch: expected %d, got %d, ignoring unlock",
				d.currentVersion,
				version,
			)
			return nil
		}
		d.currentVersion = version

		// Release every type accumulated since the last UNLOCK. This
		// covers the compressed-rounds case where the placement server
		// emitted multiple LOCK+UPDATE pairs before a single trailing
		// UNLOCK.
		toUnlock := make([]string, 0, len(d.roundChangedTypes))
		for t := range d.roundChangedTypes {
			toUnlock = append(toUnlock, t)
		}

		log.Infof("Dissemination complete for version %d (changed types %v), unlocking disseminator %s/%s",
			version, toUnlock, d.namespace, d.id,
		)

		d.currentOperation = loops.OrderUnlock
		clear(d.roundChangedTypes)
		d.completeRound(ctx)

		d.unlockRoundTypes(toUnlock)

		d.streamLoop.Enqueue(&loops.StreamSend{
			Ack: &loops.Ack{
				Op:      loops.OrderUnlock,
				Version: d.currentVersion,
			},
		})

		d.healthTarget.Ready()
		d.ready.Store(true)

	default:
		d.streamLoop.Close(&loops.Shutdown{
			Error: fmt.Errorf("unknown operation: %s", order.Order.Op),
		})
		return nil
	}

	return nil
}
