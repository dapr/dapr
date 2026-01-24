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
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/ptr"
)

const (
	operationLock   = "lock"
	operationUpdate = "update"
	operationUnlock = "unlock"
)

func (d *disseminator) handleLookupRequest(req *loops.LookupRequest) {
	d.inflight.AcquireLookup(req)
}

func (d *disseminator) handleAcquireRequest(req *loops.LockRequest) {
	d.inflight.Acquire(req)
}

func (d *disseminator) handleReportHost(report *loops.ReportHost) {
	d.reloadTypes = true
	report.Host.Version = nil
	report.Host.Operation = v1pb.HostOperation_REPORT
	d.streamLoop.Enqueue(&loops.StreamSend{
		Host: report.Host,
	})
}

func (d *disseminator) handleOrder(ctx context.Context, order *loops.StreamOrder) error {
	log.Debugf("Handling placement order=%s version=%d", order.Order.GetOperation(), order.Order.GetVersion())

	switch order.Order.GetOperation() {
	case operationLock:
		d.currentOperation = v1pb.HostOperation_LOCK
		d.currentVersion = order.Order.GetVersion()
		d.inflight.Lock(errors.New("disseminator lock in progress"))

		d.streamLoop.Enqueue(&loops.StreamSend{
			Host: &v1pb.Host{
				Operation: v1pb.HostOperation_LOCK,
				Version:   ptr.Of(d.currentVersion),
				Namespace: d.namespace,
				Id:        d.id,
			},
		})

	case operationUpdate:
		if d.currentVersion > order.Order.GetVersion() {
			return fmt.Errorf("version mismatch: expected %d, got %d",
				d.currentVersion,
				order.Order.GetVersion(),
			)
		}

		d.inflight.Set(order.Order.GetTables())
		d.currentOperation = v1pb.HostOperation_UPDATE
		d.streamLoop.Enqueue(&loops.StreamSend{
			Host: &v1pb.Host{
				Operation: v1pb.HostOperation_UPDATE,
				Version:   ptr.Of(d.currentVersion),
				Namespace: d.namespace,
				Id:        d.id,
			},
		})

	case operationUnlock:
		if d.currentOperation != v1pb.HostOperation_UPDATE {
			log.Warnf("Invalid operation sequence: expected UPDATE before UNLOCK, ignoring unlock")
			return nil
		}

		if d.currentVersion > order.Order.GetVersion() {
			log.Errorf("Version mismatch: expected %d, got %d, ignoring unlock",
				d.currentVersion,
				order.Order.GetVersion(),
			)
			return nil
		}

		log.Infof("Dissemination complete for version %d, unlocking disseminator %s/%s",
			order.Order.GetVersion(), d.namespace, d.id,
		)
		if err := d.actorTable.HaltNonHosted(ctx, d.inflight.IsActorHostedNoLock); err != nil {
			log.Errorf("Error draining non-hosted actors: %s", err)
		}

		d.currentOperation = v1pb.HostOperation_UNLOCK
		if d.reloadTypes {
			d.scheduler.ReloadActorTypes(d.actorTable.Types())
			d.reloadTypes = false
		}

		d.inflight.Unlock(ctx)

		d.streamLoop.Enqueue(&loops.StreamSend{
			Host: &v1pb.Host{
				Operation: v1pb.HostOperation_UNLOCK,
				Version:   ptr.Of(d.currentVersion),
				Namespace: d.namespace,
				Id:        d.id,
			},
		})

		d.healthTarget.Ready()

	default:
		return fmt.Errorf("unknown operation: %s", order.Order.GetOperation())
	}

	return nil
}
