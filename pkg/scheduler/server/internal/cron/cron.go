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
	"fmt"
	"strings"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	etcdcron "github.com/diagridio/go-etcd-cron/cron"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"

	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.cron")

type Options struct {
	ReplicaCount uint32
	ReplicaID    uint32
	Config       *embed.Config
	Healthz      healthz.Healthz
}

// Interface manages the cron framework, exposing a client to schedule jobs.
type Interface interface {
	// Run starts the cron server, blocking until the context is canceled.
	Run(ctx context.Context) error

	// Client returns a client to schedule jobs with the underlying cron
	// framework and database. Blocks until Etcd and the Cron library are ready.
	Client(ctx context.Context) (api.Interface, error)

	// AddWatch adds a watch for jobs to the connection pool.
	AddWatch(*schedulerv1pb.WatchJobsRequestInitial, schedulerv1pb.Scheduler_WatchJobsServer) error
}

type cron struct {
	replicaCount uint32
	replicaID    uint32

	config         *embed.Config
	connectionPool *pool.Pool
	etcdcron       api.Interface

	readyCh chan struct{}
	hzETCD  healthz.Target
}

func New(opts Options) Interface {
	return &cron{
		replicaCount: opts.ReplicaCount,
		replicaID:    opts.ReplicaID,
		config:       opts.Config,
		readyCh:      make(chan struct{}),
		hzETCD:       opts.Healthz.AddTarget(),
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

	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
	})
	if err != nil {
		return err
	}

	// pass in initial cluster endpoints, but with client ports
	c.etcdcron, err = etcdcron.New(etcdcron.Options{
		Client:         client,
		Namespace:      "dapr",
		PartitionID:    c.replicaID,
		PartitionTotal: c.replicaCount,
		TriggerFn:      c.triggerJob,
	})
	if err != nil {
		return fmt.Errorf("fail to create etcd-cron: %s", err)
	}

	c.connectionPool = pool.New(c.etcdcron)
	close(c.readyCh)

	c.hzETCD.Ready()

	return concurrency.NewRunnerManager(
		c.connectionPool.Run,
		c.etcdcron.Run,
		func(ctx context.Context) error {
			defer log.Info("EtcdCron shutting down")
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

// AddWatch adds a watch for jobs to the connection pool.
func (c *cron) AddWatch(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer) error {
	select {
	case <-c.readyCh:
		return c.connectionPool.Add(req, stream)
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
}

func (c *cron) triggerJob(ctx context.Context, req *api.TriggerRequest) *api.TriggerResponse {
	log.Debugf("Triggering job: %s", req.GetName())

	ctx, cancel := context.WithTimeout(ctx, time.Second*45)
	defer cancel()

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
