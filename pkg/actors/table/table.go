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

package table

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/idler"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/broadcaster"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actor.table")

//nolint:interfacebloat
type Interface interface {
	io.Closer

	Types() []string
	IsActorTypeHosted(actorType string) bool
	HostedTarget(actorType, actorKey string) (targets.Interface, bool)
	GetOrCreate(actorType, actorID string) (targets.Interface, bool, error)
	RegisterActorTypes(opts RegisterActorTypeOptions)
	UnRegisterActorTypes(actorTypes ...string) error
	SubscribeToTypeUpdates(ctx context.Context) (<-chan []string, []string)
	HaltAll() error
	HaltIdlable(ctx context.Context, target targets.Idlable) error
	Drain(fn func(target targets.Interface) bool) error
	Len() map[string]int

	DeleteFromTableIn(actor targets.Interface, in time.Duration)
	RemoveIdler(actor targets.Interface)
}

type Options struct {
	IdlerQueue      *queue.Processor[string, targets.Idlable]
	ReentrancyStore *reentrancystore.Store
}

type ActorTypeFactory struct {
	Type       string
	Reentrancy config.ReentrancyConfig
	Factory    targets.Factory
}

type ActorHostOptions struct {
	EntityConfigs           map[string]api.EntityConfig
	DrainRebalancedActors   bool
	DrainOngoingCallTimeout time.Duration
}

type RegisterActorTypeOptions struct {
	HostOptions *ActorHostOptions
	Factories   []ActorTypeFactory
}

type table struct {
	factories   sync.Map
	table       sync.Map
	typeUpdates *broadcaster.Broadcaster[[]string]

	drainRebalancedActors   bool
	entityConfigs           map[string]api.EntityConfig
	drainOngoingCallTimeout time.Duration
	idlerQueue              *queue.Processor[string, targets.Idlable]

	reentrancyStore *reentrancystore.Store

	lock  sync.RWMutex
	clock clock.Clock
}

func New(opts Options) Interface {
	return &table{
		drainRebalancedActors:   true,
		drainOngoingCallTimeout: time.Minute,
		entityConfigs:           make(map[string]api.EntityConfig),
		clock:                   clock.RealClock{},
		typeUpdates:             broadcaster.New[[]string](),
		idlerQueue:              opts.IdlerQueue,
		reentrancyStore:         opts.ReentrancyStore,
	}
}

func (t *table) Close() error {
	t.typeUpdates.Close()
	return nil
}

func (t *table) Types() []string {
	var keys []string
	t.factories.Range(func(key, _ any) bool {
		keys = append(keys, key.(string))
		return true
	})
	return keys
}

func (t *table) Len() map[string]int {
	alen := make(map[string]int)
	for _, atype := range t.Types() {
		alen[atype] = 0
	}

	t.table.Range(func(_, target any) bool {
		alen[target.(targets.Interface).Type()]++
		return true
	})

	return alen
}

// HaltAll halts all actors in the table, without respecting the
// drainRebalancedActors configuration, i.e. immediately deactivating all
// active actors in the table all at once.
func (t *table) HaltAll() error {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.doHaltAll(false, func(targets.Interface) bool { return true })
}

// Drain will gracefully drain all actors in the table that match the given
// function, respecting the drainRebalancedActors configuration.
func (t *table) Drain(fn func(target targets.Interface) bool) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.doHaltAll(true, fn)
}

func (t *table) doHaltAll(drain bool, fn func(target targets.Interface) bool) error {
	var (
		wg   sync.WaitGroup
		errs = slice.New[error]()
	)
	t.table.Range(func(_, target any) bool {
		wg.Add(1)
		go func(target targets.Interface) {
			defer wg.Done()
			if fn(target) {
				errs.Append(t.haltSingle(target, drain))
			}
		}(target.(targets.Interface))
		return true
	})

	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (t *table) IsActorTypeHosted(actorType string) bool {
	_, ok := t.factories.Load(actorType)
	return ok
}

func (t *table) HostedTarget(actorType, actorID string) (targets.Interface, bool) {
	v, ok := t.table.Load(key.ConstructComposite(actorType, actorID))
	if ok {
		return v.(targets.Interface), ok
	}
	return nil, ok
}

func (t *table) GetOrCreate(actorType, actorID string) (targets.Interface, bool, error) {
	akey := key.ConstructComposite(actorType, actorID)

	load, ok := t.table.Load(akey)
	if ok {
		return load.(targets.Interface), false, nil
	}

	factory, ok := t.factories.Load(actorType)
	if !ok {
		return nil, false, fmt.Errorf("%w: actor type %s not registered", actorerrors.ErrCreatingActor, actorType)
	}

	target := factory.(targets.Factory)(actorID)
	got, loaded := t.table.LoadOrStore(akey, target)
	if loaded {
		// We are optimizing for lock contention over actor factory creation, since
		// we cache actor structs anyway so memory allocations is the less of a
		// concerns.
		// If it was the case that we lost the race to store a new target, we
		// simply deactivate the one we made and push the struct on the cache pool
		// via this Deactivate.
		target.Deactivate(context.Background())
	}

	return got.(targets.Interface), true, nil
}

func (t *table) RegisterActorTypes(opts RegisterActorTypeOptions) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if opts := opts.HostOptions; opts != nil {
		t.drainOngoingCallTimeout = opts.DrainOngoingCallTimeout
		t.drainRebalancedActors = opts.DrainRebalancedActors
		t.entityConfigs = opts.EntityConfigs
	}

	for _, opt := range opts.Factories {
		t.reentrancyStore.Store(opt.Type, opt.Reentrancy)
		t.factories.Store(opt.Type, opt.Factory)
	}

	t.typeUpdates.Broadcast(t.Types())
}

func (t *table) UnRegisterActorTypes(actorTypes ...string) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	for _, atype := range actorTypes {
		t.factories.Delete(atype)
		t.reentrancyStore.Delete(atype)
	}

	err := t.doHaltAll(false, func(target targets.Interface) bool {
		return slices.Contains(actorTypes, target.Type())
	})

	t.typeUpdates.Broadcast(t.Types())

	return err
}

func (t *table) HaltIdlable(ctx context.Context, target targets.Idlable) error {
	return t.haltSingle(target, false)
}

func (t *table) DeleteFromTableIn(actor targets.Interface, in time.Duration) {
	t.idlerQueue.Enqueue(idler.New(actor, in))
}

func (t *table) RemoveIdler(actor targets.Interface) {
	t.idlerQueue.Dequeue(actor.Key())
}

func (t *table) SubscribeToTypeUpdates(ctx context.Context) (<-chan []string, []string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	ch := make(chan []string)
	t.typeUpdates.Subscribe(ctx, ch)
	return ch, t.Types()
}

func (t *table) haltSingle(target targets.Interface, drain bool) error {
	key := target.Key()

	if drain {
		drain = t.drainRebalancedActors
		if v, ok := t.entityConfigs[target.Type()]; ok {
			drain = v.DrainRebalancedActors
		}
	}

	diag.DefaultMonitoring.ActorRebalanced(target.Type())

	log.Debugf("Halting actor '%s'", key)

	// Remove the actor from the table
	// This will forbid more state changes
	got, ok := t.table.LoadAndDelete(key)

	// If nothing was loaded, the actor was probably already deactivated
	if !ok || got == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.drainOngoingCallTimeout)
	defer cancel()

	if !drain {
		cancel()
	}

	return got.(targets.Interface).Deactivate(ctx)
}
