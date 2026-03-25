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
	"sync"

	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/kit/events/broadcaster"
)

// leadership processes leadership updates from go-etcd-cron. It unmarshals the
// host addresses and broadcasts them to WatchHosts subscribers.
type leadership struct {
	hostBroadcaster *broadcaster.Broadcaster[[]*schedulerv1pb.Host]
	lock            *sync.RWMutex
	currHosts       *[]*schedulerv1pb.Host
	readyCh         chan struct{}
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

	h.lock.Lock()
	*h.currHosts = hosts

	select {
	case <-h.readyCh:
	default:
		close(h.readyCh)
		log.Info("Cron is ready")
	}
	h.lock.Unlock()

	h.hostBroadcaster.Broadcast(hosts)

	return nil
}
