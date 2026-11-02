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
	"sync/atomic"

	"github.com/diagridio/go-etcd-cron/api"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// flight is a pull trigger dispatched to a sidecar and not yet resolved. done
// is set once by whichever of the result or the repark gets there first; the
// hooks run on stream goroutines, everything else on the loop.
type flight struct {
	sidecar   string
	actorType string
	key       string
	req       *loops.TriggerRequest
	gateKeys  []string
	done      atomic.Bool
}

// repark queues a trigger the sidecar refused for lack of a slot again,
// without resolving it to the cron. The sidecar was full, so its credit is
// zero until it reports otherwise.
func (d *Dispatcher) repark(f *flight, cronFn func(api.TriggerResponseResult), gateKeys []string) {
	d.endFlight(f)
	if p := d.poolOf(f.actorType, f.sidecar); p != nil {
		p.credit = 0
	}
	d.opts.ReleaseGates(gateKeys)
	// A fresh request: a closed stream may still hold the refused one, whose
	// hooks are now no-ops.
	req := &loops.TriggerRequest{Job: f.req.Job, ResultFn: cronFn}
	if len(d.pools[f.actorType]) == 0 {
		d.opts.Requeue(req)
		return
	}
	if dispatched, throttledKey := d.tryDispatch(req); !dispatched {
		d.park(req, throttledKey, true)
	}
}

func (d *Dispatcher) isCurrent(f *flight) bool {
	sc, ok := d.sidecars[f.sidecar]
	return ok && sc.flights[f.key] == f
}

// endFlight forgets a resolved or reparked flight.
func (d *Dispatcher) endFlight(f *flight) {
	if !d.isCurrent(f) {
		return
	}
	delete(d.sidecars[f.sidecar].flights, f.key)
	d.inflight.add(f.req, -1)
	d.maybeDropSidecar(f.sidecar)
}
