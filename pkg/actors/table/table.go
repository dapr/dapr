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
	Halt(ctx context.Context, actorType, actorID string) error
	HaltIdlable(ctx context.Context, target targets.Idlable) error
	Drain(fn func(actorType, actorID string) bool)
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
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.factories.Keys()
}

func (t *table) Len() map[string]int {
	t.lock.RLock()
	defer t.lock.RUnlock()

	alen := make(map[string]int)
	for _, atype := range t.factories.Keys() {
		alen[atype] = 0
	}

	t.table.Range(func(akey string, _ targets.Interface) bool {
		atype, _ := key.ActorTypeAndIDFromKey(akey)
		alen[atype]++
		return true
	})

	return alen
}

// Drain drains actors who return true on the given func.
func (t *table) Drain(fn func(actorType, actorID string) bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	var wg sync.WaitGroup
	t.table.Range(func(actorKey string, _ targets.Interface) bool {
		wg.Add(1)
		go func(actorKey string) {
			defer wg.Done()
			actorType, actorID := key.ActorTypeAndIDFromKey(actorKey)
			if fn(actorType, actorID) {
				t.drain(actorKey, actorType)
			}
		}(actorKey)

		return true
	})

	wg.Wait()
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

	var (
		wg   sync.WaitGroup
		errs = slice.New[error]()
	)

	t.table.Range(func(actorKey string, target targets.Interface) bool {
		atype, _ := key.ActorTypeAndIDFromKey(actorKey)
		if slices.Contains(actorTypes, atype) {
			wg.Add(1)
			go func(actorKey string) {
				defer wg.Done()
				errs.Append(t.haltInLock(actorKey))
			}(actorKey)
		}

		return true
	})

	wg.Wait()

	t.typeUpdates.Batch(0, t.factories.Keys())

	return errors.Join(errs.Slice()...)
}

func (t *table) HaltIdlable(ctx context.Context, target targets.Idlable) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.idlerQueue.Dequeue(target.Key())
	t.table.Delete(target.Key())
	return target.Deactivate(ctx)
}

func (t *table) Halt(ctx context.Context, actorType, actorID string) error {
	t.lock.Lock()
	defer t.lock.Unlock()
	key := actorType + api.DaprSeparator + actorID
	target, ok := t.table.LoadAndDelete(key)
	if ok {
		log.Debugf("Halting actor '%s'", key)
		return target.Deactivate(ctx)
	}
	return nil
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
	t.lock.Lock()
	defer t.lock.Unlock()
	t.idlerQueue.Dequeue(actor.Key())
}

// HaltAll halts all actors currently in the table.
func (t *table) HaltAll() error {
	t.lock.Lock()
	defer t.lock.Unlock()

	var (
		wg   sync.WaitGroup
		errs = slice.New[error]()
	)
	wg.Add(t.table.Len())
	t.table.Range(func(actorKey string, _ targets.Interface) bool {
		go func(actorKey string) {
			defer wg.Done()
			err := t.haltInLock(actorKey)
			if err != nil {
				errs.Append(fmt.Errorf("failed to deactivate actor '%s': %v", actorKey, err))
				return
			}
		}(actorKey)
		return true
	})

	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (t *table) haltInLock(actorKey string) error {
	log.Debugf("Halting actor '%s'", actorKey)
	t.idlerQueue.Dequeue(actorKey)

	// Remove the actor from the table
	// This will forbit more state changes
	target, ok := t.table.LoadAndDelete(actorKey)

	// If nothing was loaded, the actor was probably already deactivated
	if !ok || target == nil {
		return nil
	}

	// This uses a background context as it should be unrelated from the caller's
	// context.
	// Once the decision to deactivate an actor has been made, we must go through
	// with it or we could have an inconsistent state.
	return target.Deactivate(context.Background())
}

func (t *table) SubscribeToTypeUpdates(ctx context.Context) (<-chan []string, []string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	ch := make(chan []string)
	t.typeUpdates.Subscribe(ctx, ch)
	return ch, t.factories.Keys()
}

func (t *table) drain(actorKey, actorType string) {
	doDrain := t.drainRebalancedActors
	if v, ok := t.entityConfigs[actorType]; ok {
		doDrain = v.DrainRebalancedActors
	}

	if doDrain {
		t.locker.CloseUntil(actorKey, t.drainOngoingCallTimeout)
	} else {
		t.locker.Close(actorKey)
	}

	diag.DefaultMonitoring.ActorRebalanced(actorType)

	err := t.haltInLock(actorKey)
	if err != nil {
		log.Errorf("Failed to deactivate actor '%s': %v", actorKey, err)
	}
}
