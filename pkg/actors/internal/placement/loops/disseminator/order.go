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
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

func (d *disseminator) handleLookupRequest(req *loops.LookupRequest) {
	d.inflight.AcquireLookup(req)
}

func (d *disseminator) handleAcquireRequest(req *loops.LockRequest) {
	d.inflight.Acquire(req)
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
		d.scheduler.ReloadActorTypes(d.actorTable.Types())

		d.inflight.UnlockTypes(toUnlock)
		clear(d.roundChangedTypes)

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
