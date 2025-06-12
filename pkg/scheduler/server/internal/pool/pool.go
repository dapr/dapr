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
	"sync"

	"github.com/diagridio/go-etcd-cron/api"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.server.pool")

// respChPool is a cache to reduce memory allocations for the ack channel.
var respChPool = sync.Pool{New: func() any {
	return make(chan api.TriggerResponseResult, 1)
}}

type Options struct {
	Cron api.Interface
}

// Pool represents a connection pool for namespace/appID separation of sidecars
// to schedulers.
type Pool struct {
	cron api.Interface

	connsLoop loop.Interface[loops.Event]
	readyCh   chan struct{}
}

func New(opts Options) *Pool {
	return &Pool{
		readyCh: make(chan struct{}),
		cron:    opts.Cron,
	}
}

func (p *Pool) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	p.connsLoop = connections.New(connections.Options{
		Cron:       p.cron,
		CancelPool: cancel,
	})

	close(p.readyCh)

	return concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			err := p.connsLoop.Run(ctx)
			return err
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			log.Info("Connection pool shutting down")
			p.connsLoop.Close(new(loops.Shutdown))
			return nil
		},
	).Run(ctx)
}

// AddConnection adds a new connection to the pool. It returns a context and an
// error.
func (p *Pool) AddConnection(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer) context.Context {
	<-p.readyCh

	ctx, cancel := context.WithCancelCause(stream.Context())
	p.connsLoop.Enqueue(&loops.ConnAdd{
		Request: req,
		Channel: stream,
		Cancel:  cancel,
	})

	return ctx
}

// Trigger triggers a job event to the pool. It returns a response result.
func (p *Pool) Trigger(ctx context.Context, job *internalsv1pb.JobEvent) api.TriggerResponseResult {
	<-p.readyCh

	respCh := respChPool.Get().(chan api.TriggerResponseResult)
	p.connsLoop.Enqueue(&loops.TriggerRequest{
		Job: job,
		ResultFn: func(resp api.TriggerResponseResult) {
			respCh <- resp
		},
	})

	resp := <-respCh
	respChPool.Put(respCh)
	return resp
}
