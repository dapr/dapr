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
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/namespaces"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler.server.pool")

type Options struct {
	Cron api.Interface

	// OnPlacementCapabilityChange is called when the number of capable or
	// incapable connected sidecars transitions between zero and non-zero.
	// Consumers re-read the counts when handling.
	OnPlacementCapabilityChange func()

	// OnPlacementAddresses is called with the placement addresses a
	// connecting sidecar was configured with.
	OnPlacementAddresses func([]string)
}

// Pool represents a connection pool for namespace/appID separation of sidecars
// to schedulers.
type Pool struct {
	cron api.Interface

	nsLoop  loop.Interface[loops.EventNS]
	readyCh chan struct{}

	// incapable/capable count connected sidecars by whether they reported
	// supports_actor_placement, for gating and latching the placement
	// advertisement.
	capLock                     sync.Mutex
	incapable                   int
	capable                     int
	onPlacementCapabilityChange func()
	onPlacementAddresses        func([]string)
}

func New(opts Options) *Pool {
	return &Pool{
		readyCh:                     make(chan struct{}),
		cron:                        opts.Cron,
		onPlacementCapabilityChange: opts.OnPlacementCapabilityChange,
		onPlacementAddresses:        opts.OnPlacementAddresses,
	}
}

func (p *Pool) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	p.nsLoop = namespaces.New(namespaces.Options{
		Cron:       p.cron,
		CancelPool: cancel,
	})

	close(p.readyCh)

	return concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			err := p.nsLoop.Run(ctx)
			return err
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			log.Info("Connection pool shutting down")
			p.nsLoop.Close(new(loops.Shutdown))
			return nil
		},
	).Run(ctx)
}

// AddConnection adds a new connection to the pool. It returns a context and an
// error.
func (p *Pool) AddConnection(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer) context.Context {
	<-p.readyCh

	ctx, cancel := context.WithCancelCause(stream.Context())

	p.trackCapability(ctx, req.GetSupportsActorPlacement())

	if p.onPlacementAddresses != nil && len(req.GetPlacementAddresses()) > 0 {
		p.onPlacementAddresses(req.GetPlacementAddresses())
	}

	p.nsLoop.Enqueue(&loops.ConnAdd{
		Request: req,
		Channel: stream,
		Cancel:  cancel,
	})

	return ctx
}

// trackCapability counts a sidecar connection's placement capability for the
// lifetime of its stream, calling the capability callback whenever either
// count transitions between zero and non-zero.
func (p *Pool) trackCapability(ctx context.Context, capable bool) {
	count := &p.incapable
	if capable {
		count = &p.capable
	}

	p.capLock.Lock()
	*count++
	// 0 -> 1: first of this kind connected. First incapable withholds the
	// placement advertisement, first capable makes it permanent.
	transition := *count == 1
	incapableNow := p.incapable
	p.capLock.Unlock()

	if !capable {
		monitoring.RecordPlacementIncapableSidecars(int64(incapableNow))
	}

	if transition && p.onPlacementCapabilityChange != nil {
		p.onPlacementCapabilityChange()
	}

	context.AfterFunc(ctx, func() {
		p.capLock.Lock()
		*count--
		// 1 -> 0: the last sidecar of this kind disconnected. The last
		// incapable one leaving lets the placement advertisement resume.
		transition := *count == 0
		incapableNow := p.incapable
		p.capLock.Unlock()

		if !capable {
			monitoring.RecordPlacementIncapableSidecars(int64(incapableNow))
		}

		if transition && p.onPlacementCapabilityChange != nil {
			p.onPlacementCapabilityChange()
		}
	})
}

// HasPlacementIncapableSidecars reports whether any connected sidecar does
// not support scheduler placement.
func (p *Pool) HasPlacementIncapableSidecars() bool {
	p.capLock.Lock()
	defer p.capLock.Unlock()
	return p.incapable > 0
}

// HasPlacementCapableSidecars reports whether any connected sidecar supports
// scheduler placement.
func (p *Pool) HasPlacementCapableSidecars() bool {
	p.capLock.Lock()
	defer p.capLock.Unlock()
	return p.capable > 0
}

// Trigger triggers a job event to the pool. It returns a response result.
func (p *Pool) Trigger(job *internalsv1pb.JobEvent, fn func(api.TriggerResponseResult)) {
	<-p.readyCh

	p.nsLoop.Enqueue(&loops.TriggerRequest{
		Job:      job,
		ResultFn: fn,
	})
}

// SetSchedulerInfo publishes the scheduler cluster size and this scheduler's
// index into the pool. The update fans out through the namespaces loop to
// every connection loop so concurrency gate shares stay in sync with
// membership changes.
func (p *Pool) SetSchedulerInfo(count, idx int32) {
	<-p.readyCh

	p.nsLoop.Enqueue(&loops.SchedulerInfoUpdate{
		Count: count,
		Idx:   idx,
	})
}
