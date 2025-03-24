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

package cron

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	etcdcron "github.com/diagridio/go-etcd-cron/cron"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/etcd"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/connection"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/broadcaster"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.cron")

type Options struct {
	ID      string
	Host    *schedulerv1pb.Host
	Healthz healthz.Healthz
	Etcd    etcd.Interface
}

// Interface manages the cron framework, exposing a client to schedule jobs.
type Interface interface {
	// Run starts the cron server, blocking until the context is canceled.
	Run(ctx context.Context) error

	// Client returns a client to schedule jobs with the underlying cron
	// framework and database. Blocks until Etcd and the Cron library are ready.
	Client(ctx context.Context) (api.Interface, error)

	// JobsWatch adds a watch for jobs to the connection pool.
	JobsWatch(*schedulerv1pb.WatchJobsRequestInitial, schedulerv1pb.Scheduler_WatchJobsServer) (context.Context, error)

	// HostsWatch adds a watch for hosts to the connection pool.
	HostsWatch(schedulerv1pb.Scheduler_WatchHostsServer) error
}

type cron struct {
	id string

	host            *schedulerv1pb.Host
	connectionPool  *pool.Pool
	etcdcron        api.Interface
	hostBroadcaster *broadcaster.Broadcaster[[]*schedulerv1pb.Host]
	lock            sync.RWMutex
	currHosts       []*schedulerv1pb.Host
	etcd            etcd.Interface

	readyCh chan struct{}
	closeCh chan struct{}
}

func New(opts Options) Interface {
	return &cron{
		id:              opts.ID,
		host:            opts.Host,
		hostBroadcaster: broadcaster.New[[]*schedulerv1pb.Host](),
		readyCh:         make(chan struct{}),
		closeCh:         make(chan struct{}),
		etcd:            opts.Etcd,
	}
}

func (c *cron) Run(ctx context.Context) error {
	client, err := c.etcd.Client(ctx)
	if err != nil {
		return err
	}

	log.Info("Starting Cron")

	watchLeadershipCh := make(chan []*anypb.Any)

	hostAny, err := anypb.New(c.host)
	if err != nil {
		return err
	}

	c.etcdcron, err = etcdcron.New(etcdcron.Options{
		Client:          client,
		Namespace:       "dapr",
		ID:              c.id,
		TriggerFn:       c.triggerHandler,
		ReplicaData:     hostAny,
		WatchLeadership: watchLeadershipCh,
	})
	if err != nil {
		return fmt.Errorf("fail to create etcd-cron: %s", err)
	}

	c.connectionPool = pool.New(c.etcdcron)

	return concurrency.NewRunnerManager(
		c.connectionPool.Run,
		c.etcdcron.Run,
		func(ctx context.Context) error {
			defer log.Info("Cron shut down")
			defer close(c.closeCh)
			defer c.hostBroadcaster.Close()

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case anyhosts, ok := <-watchLeadershipCh:
					if !ok {
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

					c.lock.Lock()
					c.currHosts = hosts

					select {
					case <-c.readyCh:
					default:
						close(c.readyCh)
					}

					c.hostBroadcaster.Broadcast(hosts)
					c.lock.Unlock()
				}
			}
		},
	).Run(ctx)
}

// Client returns the Cron client, blocking until Etcd and the Cron library are
// ready.
func (c *cron) Client(ctx context.Context) (api.Interface, error) {
	select {
	case <-c.readyCh:
		return c.etcdcron, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// JobsWatch adds a watch for jobs to the connection pool.
func (c *cron) JobsWatch(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer) (context.Context, error) {
	select {
	case <-c.readyCh:
		return c.connectionPool.Add(req, stream)
	case <-stream.Context().Done():
		return nil, stream.Context().Err()
	}
}

// HostsWatch adds a watch for hosts to the connection pool.
func (c *cron) HostsWatch(stream schedulerv1pb.Scheduler_WatchHostsServer) error {
	select {
	case <-c.readyCh:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}

	watchHostsCh := make(chan []*schedulerv1pb.Host)
	c.hostBroadcaster.Subscribe(stream.Context(), watchHostsCh)

	// Always send the current hosts initially to catch up to broadcast
	// subscribe.
	c.lock.RLock()
	hosts := slices.Clone(c.currHosts)
	c.lock.RUnlock()
	err := stream.Send(&schedulerv1pb.WatchHostsResponse{
		Hosts: hosts,
	})
	if err != nil {
		return err
	}

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-c.closeCh:
			return errors.New("cron closed")
		case hosts, ok := <-watchHostsCh:
			if !ok {
				return nil
			}
			if err := stream.Send(&schedulerv1pb.WatchHostsResponse{
				Hosts: hosts,
			}); err != nil {
				return err
			}
		}
	}
}

func (c *cron) triggerHandler(ctx context.Context, req *api.TriggerRequest) *api.TriggerResponse {
	log.Debugf("Triggering job: %s", req.GetName())

	start := time.Now()
	resp := c.triggerJob(ctx, req)
	monitoring.RecordTriggerDuration(start)

	log.Debugf("Triggered job %s in %s (%s)", req.GetName(), time.Since(start), resp.GetResult())

	return resp
}

func (c *cron) triggerJob(ctx context.Context, req *api.TriggerRequest) *api.TriggerResponse {
	var meta schedulerv1pb.JobMetadata
	if err := req.GetMetadata().UnmarshalTo(&meta); err != nil {
		log.Errorf("Error unmarshalling metadata: %s", err)
		return &api.TriggerResponse{Result: api.TriggerResponseResult_UNDELIVERABLE}
	}

	idx := strings.LastIndex(req.GetName(), "||")
	if idx == -1 || len(req.GetName()) <= idx+2 {
		log.Errorf("Job name is malformed: %s", req.GetName())
		return &api.TriggerResponse{Result: api.TriggerResponseResult_UNDELIVERABLE}
	}

	defer monitoring.RecordJobsTriggeredCount(&meta)
	return &api.TriggerResponse{
		Result: c.connectionPool.Send(ctx, &connection.JobEvent{
			Name:     req.GetName()[idx+2:],
			Data:     req.GetPayload(),
			Metadata: &meta,
		}),
	}
}
