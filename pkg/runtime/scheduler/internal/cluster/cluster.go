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
	"errors"
	"fmt"
	"io"

	"github.com/dapr/dapr/pkg/actors"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.cluster")

type Options struct {
	Namespace  string
	AppID      string
	AppTarget  bool
	ActorTypes []string

	Clients  []schedulerv1pb.SchedulerClient
	Actors   actors.Interface
	Channels *channels.Channels
	WFEngine wfengine.Interface
}

// Cluster manages connections to multiple schedulers.
type Cluster struct {
	namespace  string
	appID      string
	appTarget  bool
	actorTypes []string

	clients  []schedulerv1pb.SchedulerClient
	actors   actors.Interface
	channels *channels.Channels
	wfengine wfengine.Interface
}

func New(opts Options) *Cluster {
	return &Cluster{
		namespace:  opts.Namespace,
		appID:      opts.AppID,
		appTarget:  opts.AppTarget,
		actorTypes: opts.ActorTypes,
		clients:    opts.Clients,
		actors:     opts.Actors,
		channels:   opts.Channels,
		wfengine:   opts.WFEngine,
	}
}

func (c *Cluster) Run(ctx context.Context) error {
	err := c.watchJobs(ctx)
	if err == nil {
		return nil
	}

	if err == io.EOF {
		log.Warnf("Received EOF, re-establishing connection: %v", err)
		return nil
	}

	if errors.Is(err, context.Canceled) {
		return nil
	}

	return fmt.Errorf("error watching scheduler jobs: %w", err)
}

// watchJobs watches for job triggers from all scheduler clients.
func (c *Cluster) watchJobs(ctx context.Context) error {
	req := &schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     c.appID,
				Namespace: c.namespace,
			},
		},
	}

	var acceptJobTypes []schedulerv1pb.JobTargetType
	if c.appTarget {
		acceptJobTypes = append(acceptJobTypes, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB)
	}

	if len(c.actorTypes) > 0 {
		acceptJobTypes = append(acceptJobTypes, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER)
		req.GetInitial().ActorTypes = c.actorTypes
	}

	req.GetInitial().AcceptJobTypes = acceptJobTypes

	if len(c.clients) == 0 {
		log.Debug("No scheduler clients available, not watching jobs")
		<-ctx.Done()
		return ctx.Err()
	}

	runners := make([]concurrency.Runner, len(c.clients))
	connectors := make([]*connector, len(c.clients))

	// Accept engine to be nil, and ignore the disabled error.
	engine, _ := c.actors.Engine(ctx)
	for i := range c.clients {
		connectors[i] = &connector{
			req:      req,
			client:   c.clients[i],
			channels: c.channels,
			actors:   engine,
			wfengine: c.wfengine,
		}
		runners[i] = connectors[i].run
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}
