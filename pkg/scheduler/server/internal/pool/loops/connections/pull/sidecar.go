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
	"strconv"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// pool is one sidecar's pull capacity for one actor type on this scheduler:
// the free slots it last reported less those dispatched since, and its live
// streams in connection order.
type pool struct {
	sidecar string
	credit  uint32
	streams []uint64
}

// sidecar is a sidecar's state on this scheduler: its live stream count and
// the pull triggers dispatched to it that are not yet resolved.
type sidecar struct {
	streams int
	flights map[string]*flight
}

// sidecarID identifies the sidecar behind a stream by its app ID and actor
// address, so its several streams to a scheduler share pull state and another
// app cannot claim them by reporting the same address. A stream without an
// actor address is its own sidecar.
func sidecarID(streamIDx uint64, appID, actorAddress string) string {
	if actorAddress != "" {
		return appID + "/" + actorAddress
	}
	return "idx:" + strconv.FormatUint(streamIDx, 10)
}

func (d *Dispatcher) sidecarFor(id string) *sidecar {
	sc, ok := d.sidecars[id]
	if !ok {
		sc = &sidecar{flights: make(map[string]*flight)}
		d.sidecars[id] = sc
	}
	return sc
}

func (d *Dispatcher) maybeDropSidecar(id string) {
	if sc, ok := d.sidecars[id]; ok && sc.streams == 0 && len(sc.flights) == 0 {
		delete(d.sidecars, id)
	}
}

// AddStream records a new stream's sidecar, the pull slots it offers for the
// actor types it hosts, and the pull reminders it reports as running.
func (d *Dispatcher) AddStream(streamIDx uint64, req *schedulerv1pb.WatchJobsRequestInitial) {
	id := sidecarID(streamIDx, req.GetAppId(), req.GetActorAddress())
	d.streams[streamIDx] = id
	sc := d.sidecarFor(id)
	sc.streams++

	listed := make(map[string]struct{}, len(req.GetInflightReminders()))
	for _, r := range req.GetInflightReminders() {
		listed[refKey(req.GetNamespace(), r)] = struct{}{}
	}
	for _, f := range sc.flights {
		if _, ok := listed[f.key]; ok {
			f.confirmed = true
			f.stopTimer()
			delete(listed, f.key)
		} else if f.orphaned {
			d.startTimer(f)
		}
	}
	// The sidecar's latest listing replaces its running claims.
	d.dropClaims(id)
	for key := range listed {
		if _, ok := d.claims[key]; !ok {
			d.claims[key] = &claim{sidecar: id}
		}
	}

	for _, ps := range req.GetPullSlots() {
		// A sidecar reports slots before its workflow actor types are
		// registered; it is eligible once it hosts the type (the stream
		// reconnects when those change).
		if !slices.Contains(req.GetActorTypes(), ps.GetActorType()) {
			continue
		}
		p := d.poolOf(ps.GetActorType(), id)
		if p == nil {
			p = &pool{sidecar: id}
			d.pools[ps.GetActorType()] = append(d.pools[ps.GetActorType()], p)
		}
		p.credit = ps.GetFree()
		p.streams = append(p.streams, streamIDx)
		log.Debugf("Stream %d of sidecar %s offers %d pull slots for actor type %s (%d eligible sidecars)", streamIDx, id, ps.GetFree(), ps.GetActorType(), len(d.pools[ps.GetActorType()]))
	}
}

// CloseStream removes a closed stream from the pull pools. When it was the
// sidecar's last stream, its orphaned flights start their reattach timers.
func (d *Dispatcher) CloseStream(streamIDx uint64) {
	for actorType, pools := range d.pools {
		pools = slices.DeleteFunc(pools, func(p *pool) bool {
			p.streams = slices.DeleteFunc(p.streams, func(idx uint64) bool { return idx == streamIDx })
			return len(p.streams) == 0
		})
		if len(pools) == 0 {
			delete(d.pools, actorType)
		} else {
			d.pools[actorType] = pools
		}
	}

	id, ok := d.streams[streamIDx]
	if !ok {
		return
	}
	delete(d.streams, streamIDx)
	sc := d.sidecars[id]
	sc.streams--
	if sc.streams == 0 {
		d.dropClaims(id)
		for _, f := range sc.flights {
			if f.orphaned {
				d.startTimer(f)
			}
		}
	}
	d.maybeDropSidecar(id)
}

// HandleSlots applies the free slots a sidecar reported.
func (d *Dispatcher) HandleSlots(e *loops.PullSlotsUpdate) {
	p := d.poolOf(e.ActorType, sidecarID(e.StreamIDx, e.AppID, e.ActorAddress))
	// A report from a stream that has since closed is older than what the
	// sidecar's live streams report.
	if p == nil || !slices.Contains(p.streams, e.StreamIDx) {
		return
	}
	grew := e.Free > p.credit
	p.credit = e.Free
	if grew {
		d.Drain()
	}
}

// HandleResult resolves a pull reminder whose result the sidecar sent by
// reference because the stream that delivered it closed.
func (d *Dispatcher) HandleResult(e *loops.PullResult) {
	// Not looked up by stream: the stream may already have closed, and its
	// sidecar still holds the flight.
	id := sidecarID(e.StreamIDx, e.AppID, e.ActorAddress)
	key := refKey(e.Namespace, e.Reminder)
	var f *flight
	if sc, ok := d.sidecars[id]; ok {
		f = sc.flights[key]
	}
	if f == nil {
		// A claimed reminder has no trigger to resolve yet: keep the result
		// for when the cron fires it again.
		if c, ok := d.claims[key]; ok && c.sidecar == id {
			if e.Status == schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY {
				delete(d.claims, key)
			} else {
				c.status = &e.Status
				// The trigger may be parked already.
				d.Drain()
			}
			return
		}
		log.Debugf("Dropping result for unknown pull reminder %s from sidecar %s", key, id)
		return
	}
	switch e.Status {
	case schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS:
		f.req.ResultFn(api.TriggerResponseResult_SUCCESS)
	case schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY:
		f.req.Repark()
	default:
		f.req.ResultFn(api.TriggerResponseResult_FAILED)
	}
}

// dropClaims forgets the reminders sidecar id claimed as running. A claim
// already holding the sidecar's result is kept until the cron fires it again.
func (d *Dispatcher) dropClaims(id string) {
	for key, c := range d.claims {
		if c.sidecar == id && c.status == nil {
			delete(d.claims, key)
		}
	}
}
