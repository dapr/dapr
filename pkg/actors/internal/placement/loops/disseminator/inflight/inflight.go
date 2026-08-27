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
	"maps"
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

	// DisseminationTimeout is daprd's own dissemination timeout, the
	// default drain clamp budget.
	DisseminationTimeout time.Duration
}

// clampKey dedupes clamp warnings to once per value change.
type clampKey struct {
	drain  time.Duration
	budget time.Duration
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
	disseminationTimeout    time.Duration
	advertisedDissTimeout   atomic.Pointer[time.Duration]
	drainOngoingCallTimeout atomic.Pointer[time.Duration]
	drainRebalancedActors   atomic.Pointer[bool]
	entityDrainConfigs      atomic.Pointer[map[string]api.EntityDrainConfig]
	clampWarned             sync.Map

	queued       map[string][]func()
	blockedTypes map[string]struct{}

	lock loop.Interface[lock.Event]
	wg   sync.WaitGroup

	hashTable         *hashing.ConsistentHashTables
	virtualNodesCache *hashing.VirtualNodesCache
}

func New(opts Options) *Inflight {
	return &Inflight{
		hostname:             opts.Hostname,
		port:                 opts.Port,
		disseminationTimeout: opts.DisseminationTimeout,
		virtualNodesCache:    hashing.NewVirtualNodesCache(),
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
		Timeout:               i.clampedGlobalDrainTimeout(),
		DrainRebalancedActors: i.drainRebalancedActors.Load(),
		PerTypeDrain:          i.perTypeDrain(nil),
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
	if entityConfigs := i.entityDrainConfigs.Load(); entityConfigs != nil {
		for _, t := range types {
			if c, ok := (*entityConfigs)[t]; ok && c.Timeout != nil {
				if perType == nil {
					perType = make(map[string]time.Duration)
				}
				perType[t] = i.clampDrain(*c.Timeout, "entities="+t)
			}
		}
	}

	done := make(chan struct{})
	i.lock.Enqueue(&lock.CancelTypes{
		Types:                 set,
		Error:                 err,
		Timeout:               i.clampedGlobalDrainTimeout(),
		PerTypeTimeouts:       perType,
		DrainRebalancedActors: i.drainRebalancedActors.Load(),
		PerTypeDrain:          i.perTypeDrain(types),
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

// SetAdvertisedDisseminateTimeout records the placement service's advertised
// dissemination timeout.
func (i *Inflight) SetAdvertisedDisseminateTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	if cur := i.advertisedDissTimeout.Load(); cur != nil && *cur == timeout {
		return
	}
	i.advertisedDissTimeout.Store(&timeout)
	log.Infof("Placement advertised a dissemination timeout of %s; actor drain timeouts are clamped against a budget of %s", timeout, i.drainBudget())
}

// drainBudget returns the smaller of daprd's own and placement's advertised
// dissemination timeout.
func (i *Inflight) drainBudget() time.Duration {
	budget := i.disseminationTimeout
	if adv := i.advertisedDissTimeout.Load(); adv != nil && (budget <= 0 || *adv < budget) {
		budget = *adv
	}
	return budget
}

// clampDrain bounds drain by the dissemination budget, warning once per
// value change.
func (i *Inflight) clampDrain(drain time.Duration, source string) time.Duration {
	budget := i.drainBudget()
	clamped, wasClamped := api.ClampDrainOngoingCallTimeout(drain, budget)
	if !wasClamped {
		i.clampWarned.Delete(source)
		return clamped
	}

	key := clampKey{drain: drain, budget: budget}
	if prev, loaded := i.clampWarned.Swap(source, key); !loaded || prev != key {
		log.Warnf("drainOngoingCallTimeout (%s) for %s meets or exceeds the dissemination timeout (%s); clamping to %s to avoid blocking placement dissemination",
			drain, source, budget, clamped)
	}

	return clamped
}

func (i *Inflight) clampedGlobalDrainTimeout() *time.Duration {
	t := i.drainOngoingCallTimeout.Load()
	if t == nil {
		return nil
	}
	clamped := i.clampDrain(*t, "global config")
	return &clamped
}

func (i *Inflight) SetDrainOngoingCallTimeout(drain *bool, timeout *time.Duration) {
	i.drainRebalancedActors.Store(drain)
	i.drainOngoingCallTimeout.Store(timeout)
}

// SetEntityDrainConfigs replaces the per-actor-type drain configuration;
// nil/empty removes all overrides.
func (i *Inflight) SetEntityDrainConfigs(configs map[string]api.EntityDrainConfig) {
	if len(configs) == 0 {
		i.entityDrainConfigs.Store(nil)
		return
	}
	cp := make(map[string]api.EntityDrainConfig, len(configs))
	maps.Copy(cp, configs)
	i.entityDrainConfigs.Store(&cp)
}

// perTypeDrain returns the per-actor-type drainRebalancedActors overrides for
// the given types, or for every configured type when types is nil.
func (i *Inflight) perTypeDrain(types []string) map[string]bool {
	entityConfigs := i.entityDrainConfigs.Load()
	if entityConfigs == nil {
		return nil
	}

	var perDrain map[string]bool
	add := func(t string, c api.EntityDrainConfig) {
		if c.DrainRebalancedActors == nil {
			return
		}
		if perDrain == nil {
			perDrain = make(map[string]bool)
		}
		perDrain[t] = *c.DrainRebalancedActors
	}

	if types == nil {
		for t, c := range *entityConfigs {
			add(t, c)
		}
		return perDrain
	}

	for _, t := range types {
		if c, ok := (*entityConfigs)[t]; ok {
			add(t, c)
		}
	}
	return perDrain
}
