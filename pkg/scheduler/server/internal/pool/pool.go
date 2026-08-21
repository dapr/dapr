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
	"net"
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

	// OnSchedulerPlacementCapabilityChange is called when the number of capable or
	// incapable connected sidecars transitions between zero and non-zero.
	// Consumers re-read the counts when handling.
	OnSchedulerPlacementCapabilityChange func()

	// OnPlacementAddressesChange is called when the set of placement
	// addresses reported by connected sidecars gains or loses an address.
	OnPlacementAddressesChange func()
}

// maxAddressesPerReport bounds a client-supplied report, so one client
// cannot grow the address set without limit.
const maxAddressesPerReport = 8

// Pool represents a connection pool for namespace/appID separation of sidecars
// to schedulers.
type Pool struct {
	cron api.Interface

	nsLoop  loop.Interface[loops.EventNS]
	readyCh chan struct{}

	// incapable/capable count connected sidecars by whether they reported
	// supports_scheduler_placement, for gating and latching the placement
	// advertisement.
	capLock                     sync.Mutex
	incapable                   int
	capable                     int
	onPlacementCapabilityChange func()

	// addrs counts the connected sidecars reporting each placement address,
	// so an address is reported only while a sidecar reporting it is
	// connected.
	addrLock                   sync.Mutex
	addrs                      map[string]int
	onPlacementAddressesChange func()
}

func New(opts Options) *Pool {
	return &Pool{
		readyCh:                     make(chan struct{}),
		cron:                        opts.Cron,
		onPlacementCapabilityChange: opts.OnSchedulerPlacementCapabilityChange,
		addrs:                       make(map[string]int),
		onPlacementAddressesChange:  opts.OnPlacementAddressesChange,
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

	p.trackAddresses(ctx, req.GetPlacementAddresses())
	p.trackCapability(ctx, req.GetSupportsSchedulerPlacement())

	p.nsLoop.Enqueue(&loops.ConnAdd{
		Request: req,
		Channel: stream,
		Cancel:  cancel,
	})

	return ctx
}

// trackCapability counts a sidecar connection's scheduler placement
// capability for the
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

// trackAddresses counts the placement addresses a sidecar reported for the
// lifetime of its stream. Malformed addresses and oversized reports are
// dropped, since the report is client supplied.
func (p *Pool) trackAddresses(ctx context.Context, reported []string) {
	if len(reported) == 0 {
		return
	}
	if len(reported) > maxAddressesPerReport {
		log.Warnf("Ignoring a report of %d placement addresses, more than the %d a sidecar is configured with", len(reported), maxAddressesPerReport)
		return
	}

	addrs := make([]string, 0, len(reported))
	seen := make(map[string]struct{}, len(reported))
	for _, addr := range reported {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			continue
		}
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		return
	}

	p.addrLock.Lock()
	changed := false
	for _, addr := range addrs {
		p.addrs[addr]++
		changed = changed || p.addrs[addr] == 1
	}
	p.addrLock.Unlock()
	if changed && p.onPlacementAddressesChange != nil {
		p.onPlacementAddressesChange()
	}

	context.AfterFunc(ctx, func() {
		p.addrLock.Lock()
		changed := false
		for _, addr := range addrs {
			p.addrs[addr]--
			if p.addrs[addr] == 0 {
				delete(p.addrs, addr)
				changed = true
			}
		}
		p.addrLock.Unlock()
		if changed && p.onPlacementAddressesChange != nil {
			p.onPlacementAddressesChange()
		}
	})
}

// PlacementAddresses returns the placement addresses reported by the
// connected sidecars.
func (p *Pool) PlacementAddresses() []string {
	p.addrLock.Lock()
	defer p.addrLock.Unlock()
	addrs := make([]string, 0, len(p.addrs))
	for addr := range p.addrs {
		addrs = append(addrs, addr)
	}
	return addrs
}

// HasSchedulerPlacementIncapableSidecars reports whether any connected sidecar does
// not support scheduler placement.
func (p *Pool) HasSchedulerPlacementIncapableSidecars() bool {
	p.capLock.Lock()
	defer p.capLock.Unlock()
	return p.incapable > 0
}

// HasSchedulerPlacementCapableSidecars reports whether any connected sidecar supports
// scheduler placement.
func (p *Pool) HasSchedulerPlacementCapableSidecars() bool {
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
