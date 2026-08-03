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

	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool"
	"github.com/dapr/kit/events/broadcaster"
)

// leadership processes leadership updates from go-etcd-cron. It unmarshals the
// host addresses, broadcasts them to WatchHosts subscribers, and pushes the
// cluster size and this scheduler's index into the connection pool so
// concurrency gates stay in sync with membership. It also derives the single
// placement leader from the leadership table.
type leadership struct {
	hostBroadcaster *broadcaster.Broadcaster[[]*schedulerv1pb.Host]
	lock            *sync.RWMutex
	currHosts       *[]*schedulerv1pb.Host
	readyCh         chan struct{}
	ownAddress      string
	pool            *pool.Pool
	placement       PlacementLeader
}

// Handle processes a single leadership update. Called sequentially by the
// events/loop for each enqueued event.
func (h *leadership) Handle(ctx context.Context, anyhosts []*anypb.Any) error {
	if ctx.Err() != nil || anyhosts == nil {
		//nolint:nilerr
		return nil
	}

	hosts := make([]*schedulerv1pb.Host, len(anyhosts))
	for i, anyhost := range anyhosts {
		var host schedulerv1pb.Host
		if err := anyhost.UnmarshalTo(&host); err != nil {
			return err
		}
		hosts[i] = &host
	}

	count, idx := schedulerPosition(hosts, h.ownAddress)
	h.pool.SetSchedulerInfo(count, idx)

	// The leader bit is stamped on the hosts here at broadcast time, never in
	// the go-etcd-cron ReplicaData: the elector treats a replica's stored
	// data changing under a live lease as a fatal leadership error.
	leaderAddr := placementLeader(hosts)
	for _, host := range hosts {
		host.Leader = host.GetAddress() == leaderAddr && leaderAddr != ""
	}

	if h.placement != nil {
		h.placement.SetLeader(leaderAddr != "" && leaderAddr == h.ownAddress)
	}

	h.lock.Lock()
	*h.currHosts = hosts

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

// placementLeader returns the address of the placement leader: the first host
// in address-sorted order which is capable of serving placement. Returns ""
// when no host is capable, in which case no leader is advertised. Filtering on
// the capability bit keeps mixed-version and mixed-flag clusters safe: a
// scheduler without placement support (or with it disabled) is never elected.
func placementLeader(hosts []*schedulerv1pb.Host) string {
	leader := ""
	for _, host := range hosts {
		if !host.GetPlacementEnabled() {
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
