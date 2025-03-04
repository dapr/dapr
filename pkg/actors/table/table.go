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
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/locker"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/idler"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/concurrency/cmap"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/batcher"
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

	DeleteFromTable(actorType, actorID string)
	DeleteFromTableIn(actor targets.Interface, in time.Duration)
	RemoveIdler(actor targets.Interface)
}

type Options struct {
	IdlerQueue      *queue.Processor[string, targets.Idlable]
	Locker          locker.Interface
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
	factories   cmap.Map[string, targets.Factory]
	table       cmap.Map[string, targets.Interface]
	typeUpdates *batcher.Batcher[int, []string]

	// actorTypesLock is a per actor type lock to prevent concurrent access to
	// the same actor.
	actorTypesLock fifo.Map[string]

	drainRebalancedActors   bool
	entityConfigs           map[string]api.EntityConfig
	drainOngoingCallTimeout time.Duration
	idlerQueue              *queue.Processor[string, targets.Idlable]

	locker          locker.Interface
	reentrancyStore *reentrancystore.Store

	lock  sync.RWMutex
	clock clock.Clock
}

func New(opts Options) Interface {
	return &table{
		drainRebalancedActors:   true,
		drainOngoingCallTimeout: time.Minute,
		entityConfigs:           make(map[string]api.EntityConfig),
		factories:               cmap.NewMap[string, targets.Factory](),
		table:                   cmap.NewMap[string, targets.Interface](),
		actorTypesLock:          fifo.NewMap[string](),
		clock:                   clock.RealClock{},
		typeUpdates:             batcher.New[int, []string](0),
		idlerQueue:              opts.IdlerQueue,
		locker:                  opts.Locker,
		reentrancyStore:         opts.ReentrancyStore,
	}
}

func (t *table) Close() error {
	t.typeUpdates.Close()
	return nil
}

func (t *table) Types() []string {
	return t.factories.Keys()
}

func (t *table) Len() map[string]int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	alen := make(map[string]int)
	for _, atype := range t.factories.Keys() {
		alen[atype] = 0
	}

	t.table.Range(func(_ string, target targets.Interface) bool {
		alen[target.Type()]++
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
	wg.Add(t.table.Len())
	t.table.Range(func(_ string, target targets.Interface) bool {
		go func(target targets.Interface) {
			defer wg.Done()
			if fn(target) {
				errs.Append(t.haltSingle(target, drain))
			}
		}(target)
		return true
	})

	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (t *table) IsActorTypeHosted(actorType string) bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	t.actorTypesLock.Lock(actorType)
	defer t.actorTypesLock.Unlock(actorType)
	_, ok := t.factories.Load(actorType)
	return ok
}

func (t *table) HostedTarget(actorType, actorID string) (targets.Interface, bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	t.actorTypesLock.Lock(actorType)
	defer t.actorTypesLock.Unlock(actorType)
	return t.table.Load(key.ConstructComposite(actorType, actorID))
}

func (t *table) GetOrCreate(actorType, actorID string) (targets.Interface, bool, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	t.actorTypesLock.Lock(actorType)
	defer t.actorTypesLock.Unlock(actorType)

	akey := key.ConstructComposite(actorType, actorID)

	target, ok := t.table.Load(akey)
	if ok {
		return target, false, nil
	}

	factory, ok := t.factories.Load(actorType)
	if !ok {
		return nil, false, fmt.Errorf("actor type %s not registered", actorType)
	}

	target = factory(actorID)
	t.table.Store(akey, target)

	return target, true, nil
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

	t.typeUpdates.Batch(0, t.factories.Keys())
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

	t.typeUpdates.Batch(0, t.factories.Keys())

	return err
}

func (t *table) HaltIdlable(ctx context.Context, target targets.Idlable) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.haltSingle(target, false)
}

func (t *table) DeleteFromTable(actorType, actorID string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.table.Delete(actorType + api.DaprSeparator + actorID)
}

func (t *table) DeleteFromTableIn(actor targets.Interface, in time.Duration) {
	t.lock.Lock()
	defer t.lock.Unlock()
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
	return ch, t.factories.Keys()
}

func (t *table) haltSingle(target targets.Interface, drain bool) error {
	key := target.Key()

	if drain {
		drain = t.drainRebalancedActors
		if v, ok := t.entityConfigs[target.Type()]; ok {
			drain = v.DrainRebalancedActors
		}
	}

	if drain {
		t.locker.CloseUntil(key, t.drainOngoingCallTimeout)
	} else {
		t.locker.Close(key)
	}

	diag.DefaultMonitoring.ActorRebalanced(target.Type())

	log.Debugf("Halting actor '%s'", key)
	t.idlerQueue.Dequeue(key)

	// Remove the actor from the table
	// This will forbit more state changes
	target, ok := t.table.LoadAndDelete(key)

	// If nothing was loaded, the actor was probably already deactivated
	if !ok || target == nil {
		return nil
	}

	return target.Deactivate()
}
