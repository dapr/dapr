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
	"strings"
	"sync"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	etcdcron "github.com/diagridio/go-etcd-cron/cron"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/broadcaster"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.cron")

type Options struct {
	ID       string
	Host     *schedulerv1pb.Host
	Config   *embed.Config
	Healthz  healthz.Healthz
	Security security.Handler
}

// Interface manages the cron framework, exposing a client to schedule jobs.
type Interface interface {
	// Run starts the cron server, blocking until the context is canceled.
	Run(ctx context.Context) error

	// Client returns a client to schedule jobs with the underlying cron
	// framework and database. Blocks until Etcd and the Cron library are ready.
	Client(ctx context.Context) (api.Interface, error)

	// JobsWatch adds a watch for jobs to the connection pool.
	JobsWatch(*schedulerv1pb.WatchJobsRequestInitial, schedulerv1pb.Scheduler_WatchJobsServer) error

	// HostsWatch adds a watch for hosts to the connection pool.
	HostsWatch(schedulerv1pb.Scheduler_WatchHostsServer) error
}

type cron struct {
	id string

	config          *embed.Config
	host            *schedulerv1pb.Host
	connectionPool  *pool.Pool
	etcdcron        api.Interface
	hostBroadcaster *broadcaster.Broadcaster[[]*schedulerv1pb.Host]
	lock            sync.RWMutex
	currHosts       []*schedulerv1pb.Host
	security        security.Handler

	readyCh chan struct{}
	closeCh chan struct{}
	hzETCD  healthz.Target
}

func New(opts Options) Interface {
	return &cron{
		id:              opts.ID,
		config:          opts.Config,
		host:            opts.Host,
		hostBroadcaster: broadcaster.New[[]*schedulerv1pb.Host](),
		security:        opts.Security,
		readyCh:         make(chan struct{}),
		closeCh:         make(chan struct{}),
		hzETCD:          opts.Healthz.AddTarget(),
	}
}

func (c *cron) Run(ctx context.Context) error {
	defer c.hzETCD.NotReady()

	log.Info("Starting etcd")

	etcd, err := embed.StartEtcd(c.config)
	if err != nil {
		return err
	}
	defer etcd.Close()

	select {
	case <-etcd.Server.ReadyNotify():
		log.Info("Etcd server is ready!")
	case <-ctx.Done():
		return ctx.Err()
	}

	log.Info("Starting EtcdCron")

	endpoints := make([]string, 0, len(etcd.Clients))
	for _, peer := range etcd.Clients {
		endpoints = append(endpoints, peer.Addr().String())
	}

	id, err := spiffeid.FromSegments(
		c.security.ControlPlaneTrustDomain(),
		"ns", c.security.ControlPlaneNamespace(), "dapr-scheduler",
	)
	if err != nil {
		return err
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
		Context:   ctx,
		TLS:       c.security.MTLSClientConfig(id),
	})
	if err != nil {
		return err
	}

	watchLeadershipCh := make(chan []*anypb.Any)

	hostAny, err := anypb.New(c.host)
	if err != nil {
		return err
	}

	// pass in initial cluster endpoints, but with client ports
	c.etcdcron, err = etcdcron.New(etcdcron.Options{
		Client:          client,
		Namespace:       "dapr",
		ID:              c.id,
		TriggerFn:       c.triggerJob,
		ReplicaData:     hostAny,
		WatchLeadership: watchLeadershipCh,
	})
	if err != nil {
		return fmt.Errorf("fail to create etcd-cron: %s", err)
	}

	c.connectionPool = pool.New(c.etcdcron)

	c.hzETCD.Ready()

	return concurrency.NewRunnerManager(
		c.connectionPool.Run,
		c.etcdcron.Run,
		func(ctx context.Context) error {
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
		func(ctx context.Context) error {
			defer log.Info("EtcdCron shutting down")
			defer close(c.closeCh)
			select {
			case err := <-etcd.Err():
				return err
			case <-ctx.Done():
				return nil
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
func (c *cron) JobsWatch(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer) error {
	select {
	case <-c.readyCh:
		return c.connectionPool.Add(req, stream)
	case <-stream.Context().Done():
		return stream.Context().Err()
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
	err := stream.Send(&schedulerv1pb.WatchHostsResponse{
		Hosts: c.currHosts,
	})
	c.lock.RUnlock()
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

func (c *cron) triggerJob(ctx context.Context, req *api.TriggerRequest) *api.TriggerResponse {
	log.Debugf("Triggering job: %s", req.GetName())

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

	now := time.Now()
	defer func() {
		monitoring.RecordTriggerDuration(now)
		monitoring.RecordJobsTriggeredCount(&meta)
	}()

	return &api.TriggerResponse{
		Result: c.connectionPool.Send(ctx, &pool.JobEvent{
			Name:     req.GetName()[idx+2:],
			Data:     req.GetPayload(),
			Metadata: &meta,
		}),
	}
}
