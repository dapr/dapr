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

package cron

import (
	"context"
	"slices"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/handoff"
	"github.com/dapr/kit/events/broadcaster"
)

// connectionPool is the view of the connection pool leadership needs.
type connectionPool interface {
	SetSchedulerInfo(count, idx int32)
	HasSchedulerPlacementIncapableSidecars() bool
	HasSchedulerPlacementCapableSidecars() bool
}

// leadership processes leadership updates from go-etcd-cron. It unmarshals the
// host addresses, broadcasts them to WatchHosts subscribers, and pushes the
// cluster size and this scheduler's index into the connection pool so
// concurrency gates stay in sync with membership. It also derives the single
// placement leader from the leadership table.
type leadership struct {
	hostBroadcaster *broadcaster.Broadcaster[[]*schedulerv1pb.Host]
	lock            *sync.RWMutex
	broadcastHosts  *[]*schedulerv1pb.Host
	readyCh         chan struct{}
	ownAddress      string
	pool            connectionPool
	placement       PlacementLeader

	// handoff, when non-nil, is the etcd-replicated handoff state shared by
	// every scheduler, with this scheduler's local view as the fallback.
	handoff handoff.Interface

	// lastCronTable is the last leadership table from cron, unstamped, so a
	// nil-event replay recomputes stamps from the original values. Loop
	// goroutine only.
	lastCronTable []*schedulerv1pb.Host

	// advertised latches once placement was advertised to a capable
	// sidecar, so a stale old sidecar joining later cannot drop every
	// placement stream. Fallback when handoff is nil.
	advertised bool

	incapableWarned     bool
	standDownWaitLogged bool
}

// Handle processes a single leadership update, sequentially per event. A nil
// event is a capability change from the connection pool: the last table is
// re-broadcast under the current sidecar capability counts.
func (h *leadership) Handle(ctx context.Context, anyhosts []*anypb.Any) error {
	if ctx.Err() != nil {
		//nolint:nilerr
		return nil
	}

	if anyhosts == nil && h.lastCronTable == nil {
		return nil
	}

	if anyhosts != nil {
		raw := make([]*schedulerv1pb.Host, len(anyhosts))
		for i, anyhost := range anyhosts {
			var host schedulerv1pb.Host
			if err := anyhost.UnmarshalTo(&host); err != nil {
				return err
			}
			raw[i] = &host
		}
		h.lastCronTable = raw
	}

	hosts := make([]*schedulerv1pb.Host, len(h.lastCronTable))
	for i, host := range h.lastCronTable {
		hosts[i] = proto.Clone(host).(*schedulerv1pb.Host)
	}

	count, idx := schedulerPosition(hosts, h.ownAddress)
	h.pool.SetSchedulerInfo(count, idx)

	// The leader bit is stamped here at broadcast time, never in the
	// go-etcd-cron ReplicaData, since the elector treats stored replica data
	// changing under a live lease as fatal.
	gateIncapable := h.pool.HasSchedulerPlacementIncapableSidecars()
	gateCapable := h.pool.HasSchedulerPlacementCapableSidecars()
	advertised := h.advertised
	awaitingStandDown := false
	if h.handoff != nil {
		gateIncapable = h.handoff.AnySchedulerPlacementIncapableSidecars()
		gateCapable = h.handoff.AnySchedulerPlacementCapableSidecars()
		advertised = h.handoff.Advertised()
		awaitingStandDown = h.handoff.PlacementPresent() && !h.handoff.PlacementStoodDown()
	}
	// Only sidecars that take placement from the scheduler open placement
	// streams, so a live stream keeps the gate capable while that sidecar's
	// jobs streams reconnect for a target type change.
	if h.placement != nil && h.placement.HasPlacementStreams() {
		gateCapable = true
	}

	// Withhold the advertisement while an old sidecar is connected anywhere
	// in the cluster, or while a present placement service has not confirmed
	// it stood down, so two placement authorities never serve at once.
	gateBlocked := !advertised && gateIncapable
	// No scheduler placement leader is advertised while the gate holds,
	// the placement service has not stood down, or no capable sidecar
	// exists to advertise to. Only the leader bit waits for that last
	// reason, so a booting sidecar reads capable-but-leaderless and waits
	// for its own registration, not no-placement-served. Once advertised,
	// the leader stays advertised while no capable sidecar is connected.
	// Sidecars briefly reconnect their jobs streams when their target types
	// change, and a withdrawn leader halts every actor.
	awaitingLeadership := gateBlocked || awaitingStandDown || (!advertised && !gateCapable)
	// The placement service is still the authority while the gate holds or
	// the stand-down is unconfirmed, so the capability bit is masked too:
	// sidecars use the placement service rather than wait, and the cluster
	// keeps a single placement authority.
	placementServiceAuthority := gateBlocked || awaitingStandDown

	// Withholding after the advertisement became permanent would drop every
	// placement stream over one stale pod, so warn instead.
	if advertised && gateIncapable {
		if !h.incapableWarned {
			h.incapableWarned = true
			log.Warn("A sidecar running an older Dapr version connected after actor placement moved to the scheduler. Its actor APIs stall unless it can reach a placement service the control plane cannot detect, such as one under a custom service name or outside the cluster, which would place its actors as a second authority. Upgrade the sidecar, and remove any such placement service.")
		}
	} else {
		h.incapableWarned = false
	}

	electedAddr := placementLeader(hosts)
	leaderAddr := electedAddr
	if awaitingLeadership {
		leaderAddr = ""
	}
	// The latch waits for a sidecar to take a placement stream, so a
	// broadcast racing another scheduler's gate entry stays revocable.
	if leaderAddr != "" && gateCapable && !advertised &&
		h.placement != nil && h.placement.HasPlacementStreams() {
		if h.handoff != nil {
			h.handoff.LatchAdvertised()
		} else {
			h.advertised = true
		}
	}

	// The elected leader is considered cutover pending while only the
	// stand-down confirmation blocks the advertisement, which the placement
	// service takes as its signal to drain and confirm.
	cutoverPending := awaitingStandDown && !gateBlocked && gateCapable && electedAddr != ""

	if awaitingStandDown && !gateBlocked && !h.standDownWaitLogged {
		h.standDownWaitLogged = true
		log.Info("Actor placement cutover is pending, waiting for the standalone placement service to stand down. Upgrading or undeploying the placement service completes the cutover.")
	} else if !awaitingStandDown {
		h.standDownWaitLogged = false
	}

	for _, host := range hosts {
		host.Leader = host.GetAddress() == leaderAddr && leaderAddr != ""
		host.PlacementCutoverPending = cutoverPending && host.GetAddress() == electedAddr
		if placementServiceAuthority {
			host.SchedulerPlacementEnabled = false
		}
	}

	if h.placement != nil {
		h.placement.SetLeader(leaderAddr != "" && leaderAddr == h.ownAddress)
	}

	// An identical recomputation is not re-broadcast: sidecars reload every
	// connection per broadcast and that churn re-triggers this loop.
	if *h.broadcastHosts != nil && slices.EqualFunc(*h.broadcastHosts, hosts,
		func(a, b *schedulerv1pb.Host) bool { return proto.Equal(a, b) }) {
		return nil
	}

	h.lock.Lock()
	*h.broadcastHosts = hosts

	select {
	case <-h.readyCh:
	default:
		close(h.readyCh)
		log.Info("Cron is ready")
	}

	h.hostBroadcaster.Broadcast(hosts)
	h.lock.Unlock()

	return nil
}

// placementLeader returns the first address-sorted host which can serve
// placement, or "" when none can.
func placementLeader(hosts []*schedulerv1pb.Host) string {
	leader := ""
	for _, host := range hosts {
		if !host.GetSchedulerPlacementEnabled() {
			continue
		}
		if leader == "" || host.GetAddress() < leader {
			leader = host.GetAddress()
		}
	}
	return leader
}

// schedulerPosition derives (count, idx) by sorting hosts by address (stable
// across schedulers) and locating ownAddress. If ownAddress is absent the
// index falls back to 0, keeping gate math safe until membership converges.
func schedulerPosition(hosts []*schedulerv1pb.Host, ownAddress string) (int32, int32) {
	//nolint:gosec
	count := int32(len(hosts))
	if count < 1 {
		return 1, 0
	}

	sorted := slices.Clone(hosts)
	slices.SortFunc(sorted, func(a, b *schedulerv1pb.Host) int {
		switch {
		case a.GetAddress() < b.GetAddress():
			return -1
		case a.GetAddress() > b.GetAddress():
			return 1
		default:
			return 0
		}
	})

	var idx int32
	for i, host := range sorted {
		if host.GetAddress() == ownAddress {
			idx = int32(i)
			break
		}
	}

	return count, idx
}
