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
// concurrency gates stay in sync with membership.
type leadership struct {
	hostBroadcaster *broadcaster.Broadcaster[[]*schedulerv1pb.Host]
	lock            *sync.RWMutex
	currHosts       *[]*schedulerv1pb.Host
	readyCh         chan struct{}
	ownAddress      string
	pool            *pool.Pool
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

// schedulerPosition derives (count, idx) by sorting hosts by address (stable
// across schedulers) and locating ownAddress. If ownAddress is absent the
// index falls back to 0, keeping gate math safe until membership converges.
func schedulerPosition(hosts []*schedulerv1pb.Host, ownAddress string) (int32, int32) {
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
