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
	"fmt"
	"maps"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/hashing/rendezvous"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight/lock"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
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
	entityDrainTimeouts     atomic.Pointer[map[string]time.Duration]

	queued       map[string][]func()
	blockedTypes map[string]struct{}

	lock loop.Interface[lock.Event]
	wg   sync.WaitGroup

	hashTable         *hashing.ConsistentHashTables
	virtualNodesCache *hashing.VirtualNodesCache

	// v2Entries are the per actor type rendezvous tables installed by the v2
	// (scheduler placement) protocol via Merge. A process uses either the v1
	// ring tables or the v2 rendezvous tables, never both.
	//
	// Un-synchronized: writes happen only on the loop goroutine, while resolve
	// is called concurrently by HaltNonHosted's per-factory goroutines. The
	// read path must stay read-only, or it needs its own synchronization.
	v2Entries map[string]*rendezvousEntry

	// versionByType are the v2 per actor type table versions, monotonic
	// within a single stream session. Reset on reconnect via ResetVersions.
	versionByType map[string]uint64
}

// rendezvousEntry is the v2 placement table of a single actor type.
type rendezvousEntry struct {
	table *rendezvous.Table
	// hosts maps host address to app ID.
	hosts map[string]string
}

func New(opts Options) *Inflight {
	return &Inflight{
		hostname:          opts.Hostname,
		port:              opts.Port,
		virtualNodesCache: hashing.NewVirtualNodesCache(),
		hashTable: &hashing.ConsistentHashTables{
			Entries: make(map[string]*hashing.Consistent),
		},
		v2Entries:     make(map[string]*rendezvousEntry),
		versionByType: make(map[string]uint64),
		queued:        make(map[string][]func()),
		blockedTypes:  make(map[string]struct{}),
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

// Merge installs the v2 partial placement tables: only the actor types
// present in the input are replaced; a type with no hosts is removed. Returns
// the actor types whose table actually changed. Errors on an unknown hash
// algorithm or a per type version regression, both of which must close the
// stream.
func (i *Inflight) Merge(in *schedulerv1pb.PlacementTables, versions map[string]uint64) ([]string, error) {
	if len(in.GetEntries()) == 0 {
		return nil, nil
	}

	if alg := in.GetHashAlgorithm(); alg != schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS {
		return nil, fmt.Errorf("unsupported placement hash algorithm: %s", alg)
	}

	var changed []string
	for actorType, table := range in.GetEntries() {
		if version, ok := versions[actorType]; ok {
			if current, cok := i.versionByType[actorType]; cok && version < current {
				return nil, fmt.Errorf("placement table version regression for actor type %s: %d < %d",
					actorType, version, current)
			}
			i.versionByType[actorType] = version
		}

		if len(table.GetHosts()) == 0 {
			if _, ok := i.v2Entries[actorType]; ok {
				delete(i.v2Entries, actorType)
				changed = append(changed, actorType)
			}
			continue
		}

		addrs := make([]string, 0, len(table.GetHosts()))
		hosts := make(map[string]string, len(table.GetHosts()))
		for addr, host := range table.GetHosts() {
			addrs = append(addrs, addr)
			hosts[addr] = host.GetAppId()
		}

		entry := &rendezvousEntry{
			table: rendezvous.New(addrs),
			hosts: hosts,
		}

		if existing, ok := i.v2Entries[actorType]; !ok ||
			!existing.table.Equal(entry.table) ||
			!maps.Equal(existing.hosts, hosts) {
			changed = append(changed, actorType)
		}

		i.v2Entries[actorType] = entry
	}

	return changed, nil
}

// HasTables returns whether a placement table is installed for every given
// actor type.
func (i *Inflight) HasTables(types []string) bool {
	for _, t := range types {
		if _, ok := i.v2Entries[t]; ok {
			continue
		}
		if _, ok := i.hashTable.Entries[t]; ok {
			continue
		}
		return false
	}
	return true
}

// ResetSession clears the per-type tables and versions on stream loss: a
// new session starts from its authoritative snapshot, which carries no
// tombstones for types deleted while disconnected. In-session removals are
// tombstones handled by Merge.
func (i *Inflight) ResetSession() {
	clear(i.v2Entries)
	clear(i.versionByType)
}

// LockTypes marks the given actor types as blocked. New acquires for these
// types queue until UnlockTypes is called for them.
func (i *Inflight) LockTypes(types []string) {
	for _, t := range types {
		i.blockedTypes[t] = struct{}{}
	}
}

// CancelClaimsForTypes drains in-flight claims for the given actor types,
// using the configured drain timeout. Per-entity drain timeouts (set via
// SetEntityDrainOngoingCallTimeouts) override the global timeout for their
// type. Blocks until drain completes. Should be called AFTER LockTypes so
// no new claims are issued for these types concurrently with the drain.
func (i *Inflight) CancelClaimsForTypes(types []string, err error) {
	if i.lock == nil || len(types) == 0 {
		return
	}
	set := make(map[string]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}

	var perType map[string]time.Duration
	if entityTimeouts := i.entityDrainTimeouts.Load(); entityTimeouts != nil {
		for _, t := range types {
			if v, ok := (*entityTimeouts)[t]; ok {
				if perType == nil {
					perType = make(map[string]time.Duration)
				}
				perType[t] = v
			}
		}
	}

	done := make(chan struct{})
	i.lock.Enqueue(&lock.CancelTypes{
		Types:                 set,
		Error:                 err,
		Timeout:               i.drainOngoingCallTimeout.Load(),
		PerTypeTimeouts:       perType,
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

// IsBlocked returns whether new acquires for the actor type currently queue
// due to an in-flight dissemination round.
func (i *Inflight) IsBlocked(actorType string) bool {
	return i.isBlocked(actorType)
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
	if entry, ok := i.v2Entries[req.ActorType]; ok {
		addr, ok := entry.table.Lookup(req.ActorID)
		if !ok {
			return nil, messages.ErrActorNoAddress.WithFormat(req.ActorKey())
		}
		return &api.LookupActorResponse{
			Address: addr,
			AppID:   entry.hosts[addr],
			Local:   loops.IsActorLocal(addr, i.hostname, i.port),
		}, nil
	}

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

// SetEntityDrainOngoingCallTimeouts replaces the per-actor-type drain
// timeouts. A nil or empty map means "no per-type overrides; fall back to
// the global timeout for every type".
func (i *Inflight) SetEntityDrainOngoingCallTimeouts(timeouts map[string]time.Duration) {
	if len(timeouts) == 0 {
		i.entityDrainTimeouts.Store(nil)
		return
	}
	cp := make(map[string]time.Duration, len(timeouts))
	maps.Copy(cp, timeouts)
	i.entityDrainTimeouts.Store(&cp)
}
