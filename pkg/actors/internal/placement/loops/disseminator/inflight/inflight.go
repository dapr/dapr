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

// Inflight gates lookup and lock-claim acquisition during placement
// dissemination rounds.
//
// Two independent mechanisms operate here:
//
//  1. A single global claim-tracking event loop (`lock`) issues a Claim per
//     accepted request. Claims are torn down with a grace period when the
//     placement stream is lost (Close), so in-flight actor invocations can
//     drain. The loop is independent of per-round dissemination phases:
//     routine LOCK/UNLOCK rounds do NOT cycle it, so legitimately-running
//     actor calls survive normal dissemination.
//
//  2. A per-actor-type queue (`queued` + `blockedTypes`) holds new requests
//     whose actor type is changing in the active dissemination round. Only
//     types whose hash ring actually changed (computed in Set) are blocked;
//     requests for unchanged types proceed immediately with the new table.
type Inflight struct {
	hostname                string
	port                    string
	drainOngoingCallTimeout atomic.Pointer[time.Duration]
	drainRebalancedActors   atomic.Pointer[bool]

	queued       map[string][]func()
	blockedTypes map[string]struct{}

	lock loop.Interface[lock.Event]
	wg   sync.WaitGroup

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
		queued:       make(map[string][]func()),
		blockedTypes: make(map[string]struct{}),
	}
}

// Close cancels the claim-tracking loop, draining in-flight claims with the
// configured grace period. Called on placement-stream loss / shutdown.
// Per-type queued requests stay queued; they will be flushed by the next
// Open + UnlockTypes after the stream re-establishes and a dissemination
// round completes.
func (i *Inflight) Close(err error) {
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

// Open ensures the claim-tracking loop is running and flushes any queued
// requests whose actor type is not currently blocked. Idempotent.
func (i *Inflight) Open(ctx context.Context) {
	if i.lock == nil {
		l := lock.New()
		i.lock = l
		i.wg.Go(func() {
			if lerr := l.Run(ctx); lerr != nil {
				log.Errorf("Inflight lock loop ended with error: %s", lerr)
			}
		})
	}

	for t, fns := range i.queued {
		if _, blocked := i.blockedTypes[t]; blocked {
			continue
		}
		for _, fn := range fns {
			fn()
		}
		delete(i.queued, t)
	}
}

// Set installs new placement tables and returns the actor types whose hash
// rings changed compared to the previous tables (including newly-added and
// removed types). The returned slice should be passed to LockTypes by the
// caller.
func (i *Inflight) Set(in *v1pb.PlacementTables, version uint64) []string {
	oldEntries := i.hashTable.Entries
	newEntries := make(map[string]*hashing.Consistent, len(in.GetEntries()))

	for k, v := range in.GetEntries() {
		loadMap := make(map[string]*hashing.Host, len(v.GetLoadMap()))
		for lk, lv := range v.GetLoadMap() {
			//nolint:staticcheck
			loadMap[lk] = hashing.NewHost(lv.GetName(), lv.GetId(), lv.GetLoad(), lv.GetPort())
		}
		newEntries[k] = hashing.NewFromExisting(loadMap, in.GetReplicationFactor(), i.virtualNodesCache)
	}

	var changed []string
	for k, newRing := range newEntries {
		if oldRing, ok := oldEntries[k]; !ok || !oldRing.Equal(newRing) {
			changed = append(changed, k)
		}
	}
	for k := range oldEntries {
		if _, ok := newEntries[k]; !ok {
			changed = append(changed, k)
		}
	}

	i.hashTable.Version = strconv.FormatUint(version, 10)
	i.hashTable.Entries = newEntries
	return changed
}

// LockTypes marks the given actor types as blocked. New acquires for these
// types queue until UnlockTypes is called for them.
func (i *Inflight) LockTypes(types []string) {
	for _, t := range types {
		i.blockedTypes[t] = struct{}{}
	}
}

// CancelClaimsForTypes drains in-flight claims for the given actor types,
// using the configured drain timeout. Blocks until drain completes. Should
// be called AFTER LockTypes so no new claims are issued for these types
// concurrently with the drain.
func (i *Inflight) CancelClaimsForTypes(types []string, err error) {
	if i.lock == nil || len(types) == 0 {
		return
	}
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	done := make(chan struct{})
	i.lock.Enqueue(&lock.CancelTypes{
		Types:                 set,
		Error:                 err,
		Timeout:               i.drainOngoingCallTimeout.Load(),
		DrainRebalancedActors: i.drainRebalancedActors.Load(),
		Done:                  done,
	})
	<-done
}

// UnlockTypes unmarks the given actor types and flushes any acquires that
// queued while they were blocked. Open must have been called previously so
// the claim-tracking loop is available.
func (i *Inflight) UnlockTypes(types []string) {
	for _, t := range types {
		delete(i.blockedTypes, t)
		fns, ok := i.queued[t]
		if !ok || i.lock == nil {
			continue
		}
		for _, fn := range fns {
			fn()
		}
		delete(i.queued, t)
	}
}

func (i *Inflight) Acquire(lu *loops.LockRequest) {
	if i.lock == nil || i.isBlocked(lu.ActorType) {
		i.queued[lu.ActorType] = append(i.queued[lu.ActorType], func() {
			lu.Response <- i.getLockResponse(lu)
		})
		return
	}

	lu.Response <- i.getLockResponse(lu)
}

func (i *Inflight) AcquireLookup(lu *loops.LookupRequest) {
	actorType := lu.Request.ActorType
	if i.lock == nil || i.isBlocked(actorType) {
		i.queued[actorType] = append(i.queued[actorType], func() {
			lu.Response <- i.getLookupResponse(lu)
		})
		return
	}

	lu.Response <- i.getLookupResponse(lu)
}

func (i *Inflight) isBlocked(actorType string) bool {
	_, ok := i.blockedTypes[actorType]
	return ok
}

func (i *Inflight) getLockResponse(lu *loops.LockRequest) *loops.LockResponse {
	aq := aquireCache.Get().(*lock.Acquire)
	aq.ActorType = lu.ActorType
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
	aq.ActorType = lu.Request.ActorType
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
		return nil, messages.ErrActorNoAddress.WithFormat(req.ActorKey())
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
