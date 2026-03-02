/*
Copyright 2026 The Dapr Authors
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

package inflight

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight/lock"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.loops.disseminator.inflight")

var aquireCache = sync.Pool{
	New: func() any {
		return &lock.Acquire{
			RespCh: make(chan *lock.Claim),
		}
	},
}

type Options struct {
	Hostname string
	Port     string
}

type Inflight struct {
	hostname                string
	port                    string
	drainOngoingCallTimeout atomic.Pointer[time.Duration]
	drainRebalancedActors   atomic.Pointer[bool]

	acquires []func()
	lock     loop.Interface[lock.Event]
	wg       sync.WaitGroup

	hashTable         *hashing.ConsistentHashTables
	virtualNodesCache *hashing.VirtualNodesCache
}

func New(opts Options) *Inflight {
	return &Inflight{
		hostname:          opts.Hostname,
		port:              opts.Port,
		virtualNodesCache: hashing.NewVirtualNodesCache(),
		hashTable: &hashing.ConsistentHashTables{
			Entries: make(map[string]*hashing.Consistent),
		},
		lock: nil,
	}
}

func (i *Inflight) Lock(err error) {
	if i.lock == nil {
		return
	}

	i.lock.Close(&lock.CloseLock{
		Error:                 err,
		Timeout:               i.drainOngoingCallTimeout.Load(),
		DrainRebalancedActors: i.drainRebalancedActors.Load(),
	})
	i.wg.Wait()
	lo := i.lock
	i.lock = nil
	lock.LoopFactory.CacheLoop(lo)
}

func (i *Inflight) Set(in *v1pb.PlacementTables, version uint64) {
	entries := make(map[string]*hashing.Consistent)

	for k, v := range in.GetEntries() {
		loadMap := make(map[string]*hashing.Host, len(v.GetLoadMap()))
		for lk, lv := range v.GetLoadMap() {
			//nolint:staticcheck
			loadMap[lk] = hashing.NewHost(lv.GetName(), lv.GetId(), lv.GetLoad(), lv.GetPort())
		}

		entries[k] = hashing.NewFromExisting(loadMap, in.GetReplicationFactor(), i.virtualNodesCache)
	}

	clear(i.hashTable.Entries)

	i.hashTable.Version = strconv.FormatUint(version, 10)
	i.hashTable.Entries = entries
}

func (i *Inflight) Unlock(ctx context.Context) {
	// Recreate the lock to allow queued requests to proceed.
	lock := lock.New()
	i.lock = lock
	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		lerr := lock.Run(ctx)
		if lerr != nil {
			log.Errorf("Inflight lock loop ended with error: %s", lerr)
		}
	}()

	for _, a := range i.acquires {
		a()
	}
	i.acquires = []func(){}
}

func (i *Inflight) Acquire(lu *loops.LockRequest) {
	// If still locked, queue the request.
	if i.lock == nil {
		i.acquires = append(i.acquires, func() {
			lu.Response <- i.getLockResponse(lu)
		})
		return
	}

	lu.Response <- i.getLockResponse(lu)
}

func (i *Inflight) AcquireLookup(lu *loops.LookupRequest) {
	// If still locked, queue the request.
	if i.lock == nil {
		i.acquires = append(i.acquires, func() {
			lu.Response <- i.getLookupResponse(lu)
		})
		return
	}

	lu.Response <- i.getLookupResponse(lu)
}

func (i *Inflight) getLockResponse(lu *loops.LockRequest) *loops.LockResponse {
	aq := aquireCache.Get().(*lock.Acquire)
	aq.Context = lu.Context

	i.lock.Enqueue(aq)
	claim := <-aq.RespCh
	aquireCache.Put(aq)

	return &loops.LockResponse{
		Context: claim.Context,
		Cancel:  claim.Cancel,
	}
}

func (i *Inflight) getLookupResponse(lu *loops.LookupRequest) *loops.LookupResponse {
	aq := aquireCache.Get().(*lock.Acquire)
	aq.Context = lu.Context

	i.lock.Enqueue(aq)
	claim := <-aq.RespCh
	aquireCache.Put(aq)

	resp, err := i.resolve(lu.Request)
	return &loops.LookupResponse{
		Context:  claim.Context,
		Cancel:   claim.Cancel,
		Response: resp,
		Error:    err,
	}
}

func (i *Inflight) IsActorHostedNoLock(req *api.LookupActorRequest) bool {
	resp, err := i.resolve(req)
	if err != nil {
		return false
	}

	return resp != nil && resp.Local
}

func (i *Inflight) resolve(req *api.LookupActorRequest) (*api.LookupActorResponse, error) {
	table, ok := i.hashTable.Entries[req.ActorType]
	if !ok {
		return nil, messages.ErrActorNoAddress
	}

	host, err := table.GetHost(req.ActorID)
	if err != nil {
		return nil, err
	}

	return &api.LookupActorResponse{
		Address: host.Name,
		AppID:   host.AppID,
		Local:   loops.IsActorLocal(host.Name, i.hostname, i.port),
	}, nil
}

func (i *Inflight) SetDrainOngoingCallTimeout(drain *bool, timeout *time.Duration) {
	i.drainRebalancedActors.Store(drain)
	i.drainOngoingCallTimeout.Store(timeout)
}
