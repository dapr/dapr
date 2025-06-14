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

package pool

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/connection"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/store"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

// Pool represents a connection pool for namespace/appID separation of sidecars to schedulers.
type Pool struct {
	cron api.Interface

	nsPool *store.Namespace

	wg      sync.WaitGroup
	closeCh chan struct{}
	running atomic.Bool
}

func New(cron api.Interface) *Pool {
	return &Pool{
		cron:    cron,
		nsPool:  store.New(),
		closeCh: make(chan struct{}),
	}
}

func (p *Pool) Run(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("pool is already running")
	}

	<-ctx.Done()
	close(p.closeCh)
	p.wg.Wait()

	return nil
}

// Add adds a connection to the pool for a given namespace/appID.
func (p *Pool) Add(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer) (context.Context, error) {
	var prefixes []string
	var appID *string

	// To account for backwards compatibility where older clients did not use
	// this field, we assume a connected client and implement both app jobs, as
	// well as actor job types. We can remove this in v1.16
	ts := req.GetAcceptJobTypes()
	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB) {
		log.Infof("Adding a Sidecar connection to Scheduler for appID: %s/%s.", req.GetNamespace(), req.GetAppId())
		appID = &req.AppId
		prefixes = append(prefixes, "app||"+req.GetNamespace()+"||"+req.GetAppId()+"||")
	}

	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER) {
		for _, actorType := range req.GetActorTypes() {
			log.Infof("Adding a Sidecar connection to Scheduler for actor type: %s/%s.", req.GetNamespace(), actorType)
			prefixes = append(prefixes, "actorreminder||"+req.GetNamespace()+"||"+actorType+"||")
		}
	}

	ctx, conn, cancel := p.nsPool.Add(stream.Context(), store.Options{
		Namespace:  req.GetNamespace(),
		AppID:      appID,
		ActorTypes: req.GetActorTypes(),
	})

	p.streamConnection(ctx, req, stream, conn)

	log.Debugf("Marking deliverable prefixes for Sidecar connection: %s/%s: %v.", req.GetNamespace(), req.GetAppId(), prefixes)

	dcancel, err := p.cron.DeliverablePrefixes(ctx, prefixes...)
	if err != nil {
		cancel(err)
		return nil, err
	}

	log.Debugf("Added a Sidecar connection to Scheduler for: %s/%s.", req.GetNamespace(), req.GetAppId())

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		select {
		case <-ctx.Done():
		case <-p.closeCh:
		}

		err := fmt.Errorf("Closing connection to %s/%s", req.GetNamespace(), req.GetAppId())

		dcancel(err)
		cancel(err)

		log.Debugf("Closed and removed connection to %s/%s", req.GetNamespace(), req.GetAppId())
	}()

	return ctx, nil
}

// Send is a blocking function that sends a job trigger to a correct job
// recipient.
func (p *Pool) Send(ctx context.Context, job *connection.JobEvent) api.TriggerResponseResult {
	conn, ok := p.getConn(job.Metadata)
	if !ok {
		return api.TriggerResponseResult_UNDELIVERABLE
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		select {
		case <-ctx.Done():
		case <-p.closeCh:
		}
		cancel()
	}()

	return conn.Handle(ctx, job)
}

// getConn returns a connection from the pool based on the metadata.
func (p *Pool) getConn(meta *schedulerv1pb.JobMetadata) (*connection.Connection, bool) {
	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		return p.nsPool.AppID(meta.GetNamespace(), meta.GetAppId())
	case *schedulerv1pb.JobTargetMetadata_Actor:
		return p.nsPool.ActorType(meta.GetNamespace(), t.GetActor().GetType())
	default:
		return nil, false
	}
}
