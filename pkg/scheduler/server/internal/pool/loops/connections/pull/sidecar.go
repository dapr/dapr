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

// AddStream records a new stream's sidecar and the pull slots it offers for
// the actor types it hosts.
func (d *Dispatcher) AddStream(streamIDx uint64, req *schedulerv1pb.WatchJobsRequestInitial) {
	id := sidecarID(streamIDx, req.GetAppId(), req.GetActorAddress())
	d.streams[streamIDx] = id
	d.sidecarFor(id).streams++

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

// CloseStream removes a closed stream from the pull pools.
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
	d.sidecars[id].streams--
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
