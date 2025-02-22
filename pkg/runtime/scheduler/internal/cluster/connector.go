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

	"github.com/dapr/dapr/pkg/actors/engine"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
)

type connector struct {
	req      *schedulerv1pb.WatchJobsRequest
	client   schedulerv1pb.SchedulerClient
	channels *channels.Channels
	actors   engine.Interface
	wfengine wfengine.Interface
}

// run starts the scheduler connector. Attempts to re-connect to the Scheduler
// to WatchJobs on non-terminal errors.
func (c *connector) run(ctx context.Context) error {
	for {
		stream, err := c.client.WatchJobs(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			log.Errorf("Failed to watch scheduler jobs, retrying: %s", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			return err
		}

		if err = stream.Send(c.req); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			log.Errorf("scheduler stream error, re-connecting: %s", err)
			return err
		}

		log.Info("Scheduler stream connected")

		err = (&streamer{
			stream:   stream,
			resultCh: make(chan *schedulerv1pb.WatchJobsRequest),
			channels: c.channels,
			actors:   c.actors,
			wfengine: c.wfengine,
		}).run(ctx)

		if err == nil {
			log.Infof("Scheduler stream disconnected")
		} else {
			log.Errorf("Scheduler stream disconnected: %v", err)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}
