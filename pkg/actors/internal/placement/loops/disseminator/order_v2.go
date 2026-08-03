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

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
)

// v2Round tracks a single in-flight v2 dissemination round, keyed by seq.
// Rounds over disjoint actor type sets may be in flight concurrently.
type v2Round struct {
	scope   []string
	changed map[string]struct{}
}

// handleOrderV2 handles a placement order on the v2 (scheduler placement)
// protocol: seq-keyed rounds scoped to actor types, with partial table
// merges. The startup snapshot is a round with an empty scope covering all
// types.
func (d *disseminator) handleOrderV2(ctx context.Context, order *loops.StreamOrder) error {
	seq := order.Order.Version

	log.Debugf("Handling placement order=%s seq=%d scope=%v", order.Order.Op, seq, order.Order.Scope)

	switch order.Order.Op {
	case loops.OrderLock:
		r := &v2Round{
			scope:   order.Order.Scope,
			changed: make(map[string]struct{}),
		}
		d.v2Rounds[seq] = r
		d.timeoutQ.Enqueue(seq)

		// The scope is known up front on v2: block lookups for the scoped
		// types immediately. An empty scope is the startup snapshot; nothing
		// blocks, the UPDATE diff decides.
		if len(r.scope) > 0 {
			d.inflight.LockTypes(r.scope)
		}

		d.streamLoop.Enqueue(&loops.StreamSend{
			Ack: &loops.Ack{Op: loops.OrderLock, Version: seq},
		})

	case loops.OrderUpdate:
		r, ok := d.v2Rounds[seq]
		if !ok {
			d.closeStreamV2(fmt.Errorf("received UPDATE for unknown round seq %d", seq))
			return nil
		}

		changed, err := d.inflight.Merge(order.Order.V2Tables, order.Order.Versions)
		if err != nil {
			d.closeStreamV2(err)
			return nil
		}

		for _, t := range changed {
			r.changed[t] = struct{}{}
		}

		d.inflight.LockTypes(changed)
		d.inflight.Open(ctx)

		// Drain in-flight claims only for actor types whose table changed so
		// the request layer retries against the new routing.
		d.inflight.CancelClaimsForTypes(changed, errors.New("placement table updated"))

		if err := d.actorTable.HaltNonHosted(ctx, d.inflight.IsActorHostedNoLock); err != nil {
			log.Errorf("Error draining non-hosted actors: %s", err)
		}

		d.streamLoop.Enqueue(&loops.StreamSend{
			Ack: &loops.Ack{Op: loops.OrderUpdate, Version: seq},
		})

	case loops.OrderUnlock:
		r, ok := d.v2Rounds[seq]
		if !ok {
			d.closeStreamV2(fmt.Errorf("received UNLOCK for unknown round seq %d", seq))
			return nil
		}

		delete(d.v2Rounds, seq)
		d.timeoutQ.Dequeue(seq)

		// Release the union of the declared scope and every type whose table
		// changed during the round.
		toUnlock := make([]string, 0, len(r.scope)+len(r.changed))
		for _, t := range r.scope {
			if _, ok := r.changed[t]; !ok {
				toUnlock = append(toUnlock, t)
			}
		}
		for t := range r.changed {
			toUnlock = append(toUnlock, t)
		}

		log.Debugf("Dissemination round seq=%d complete (types %v), unlocking %s/%s",
			seq, toUnlock, d.namespace, d.id)

		d.scheduler.ReloadActorTypes(d.actorTable.Types())
		d.inflight.UnlockTypes(toUnlock)

		d.streamLoop.Enqueue(&loops.StreamSend{
			Ack: &loops.Ack{Op: loops.OrderUnlock, Version: seq},
		})

		// The sidecar is ready only once it has a placement table for every
		// actor type it locally hosts.
		if d.inflight.HasTables(d.actorTable.Types()) {
			d.healthTarget.Ready()
			d.ready.Store(true)
		}

	default:
		d.closeStreamV2(fmt.Errorf("unknown operation: %s", order.Order.Op))
	}

	return nil
}

func (d *disseminator) closeStreamV2(err error) {
	d.streamLoop.Close(&loops.Shutdown{Error: err})
}
