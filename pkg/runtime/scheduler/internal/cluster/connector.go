/*
Copyright 2024 The Dapr Authors
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

package cluster

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/router"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
)

type connector struct {
	req      *schedulerv1pb.WatchJobsRequest
	client   schedulerv1pb.SchedulerClient
	channels *channels.Channels
	actors   router.Interface
	wfengine wfengine.Interface

	// addr is the scheduler address of client. pull and pullActorType are
	// handed to each streamer; see streamer.pull.
	addr          string
	pull          *Pull
	pullActorType string
}

// run starts the scheduler connector, retrying on failures so that a single
// scheduler connection error does not tear down the other healthy connections
// managed by the same RunnerManager.
func (c *connector) run(ctx context.Context) error {
	var failCount int
	for {
		stream, err := c.client.WatchJobs(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			failCount++
			if failCount == 1 {
				log.Errorf("Failed to watch scheduler jobs, retrying: %s", err)
			} else {
				log.Debugf("Failed to watch scheduler jobs (attempt %d), retrying: %s", failCount, err)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retry.Jitter(time.Second/2, time.Second/4)):
				continue
			}
		}

		if err = stream.Send(c.initial()); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			failCount++
			if failCount == 1 {
				log.Errorf("Scheduler stream error, re-connecting: %s", err)
			} else {
				log.Debugf("Scheduler stream error (attempt %d), re-connecting: %s", failCount, err)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retry.Jitter(time.Second/2, time.Second/4)):
				continue
			}
		}

		failCount = 0
		log.Infof("Scheduler stream connected for %s", c.req.GetInitial().GetAcceptJobTypes())

		err = (&streamer{
			stream:   stream,
			resultCh: make(chan *schedulerv1pb.WatchJobsRequest),
			channels: c.channels,
			actors:   c.actors,
			wfengine: c.wfengine,

			addr:          c.addr,
			pull:          c.pull,
			pullActorType: c.pullActorType,
		}).run(ctx)
		if err == nil {
			log.Infof("Scheduler stream disconnected")
		} else {
			log.Errorf("Scheduler stream disconnected: %v", err)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retry.Jitter(time.Second/2, time.Second/4)):
		}
	}
}

// initial returns the request that opens a stream. With pull dispatch it
// carries the current free slots and the pull reminders this sidecar still
// holds from earlier streams to the same scheduler.
func (c *connector) initial() *schedulerv1pb.WatchJobsRequest {
	if c.pull == nil {
		return c.req
	}
	req := proto.Clone(c.req).(*schedulerv1pb.WatchJobsRequest)
	free, _ := c.wfengine.ActivityFreeSlots()
	req.GetInitial().PullSlots = []*schedulerv1pb.PullSlots{{ActorType: c.pullActorType, Free: free}}
	req.GetInitial().InflightReminders = c.pull.refs(c.addr)
	return req
}
