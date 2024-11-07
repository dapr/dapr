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
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/targets"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/concurrency/cmap"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/batcher"
	"github.com/dapr/kit/logger"
	"k8s.io/utils/clock"
)

type Interface interface {
	io.Closer

	Types() []string
	IsActorTypeHosted(actorType string) bool
	HostedTarget(actorType, actorKey string) (targets.Interface, bool)
	GetOrCreate(actorType, actorID string) (targets.Interface, bool, error)
	RegisterActorType(actorType string, factory targets.Factory)
	UnRegisterActorType(actorType string) error
	SubscribeToTypeUpdates(ctx context.Context) <-chan []string
	HaltAll() error
	Drain(fn func(actorType, actorID string) bool)
	Len() map[string]int
}

var log = logger.NewLogger("dapr.runtime.actor.table")

type Options struct {
	EntityConfigs           map[string]requestresponse.EntityConfig
	DrainRebalancedActors   bool
	DrainOngoingCallTimeout time.Duration
}

type table struct {
	factories   cmap.Map[string, targets.Factory]
	table       cmap.Map[string, targets.Interface]
	typeUpdates *batcher.Batcher[int, []string]

	// actorTypesLock is a per actor type lock to prevent concurrent access to
	// the same actor.
	actorTypesLock fifo.Map[string]

	drainRebalancedActors   bool
	entityConfigs           map[string]requestresponse.EntityConfig
	drainOngoingCallTimeout time.Duration

	lock  sync.RWMutex
	clock clock.Clock
}

func New(opts Options) Interface {
	return &table{
		entityConfigs:           opts.EntityConfigs,
		drainRebalancedActors:   opts.DrainRebalancedActors,
		drainOngoingCallTimeout: opts.DrainOngoingCallTimeout,
		factories:               cmap.NewMap[string, targets.Factory](),
		table:                   cmap.NewMap[string, targets.Interface](),
		actorTypesLock:          fifo.NewMap[string](),
		clock:                   clock.RealClock{},
		typeUpdates:             batcher.New[int, []string](0),
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
	for atype := range alen {
		alen[atype]++
	}
	return alen
}

// Drain drains actors who return true on the given func.
func (t *table) Drain(fn func(actorType, actorID string) bool) {
	t.lock.Lock()
	defer t.lock.Unlock()

	var wg sync.WaitGroup
	t.table.Range(func(actorKey string, target targets.Interface) bool {
		wg.Add(1)
		go func(actorKey string, target targets.Interface) {
			actorType, actorID := key.ActorTypeAndIDFromKey(actorKey)
			defer wg.Done()
			if fn(actorType, actorID) {
				t.drain(actorType, actorID, target)
			}
		}(actorKey, target)

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

	target, ok := t.table.Load(key.ConstructComposite(actorType, actorID))
	if ok {
		return target, false, nil
	}

	factory, ok := t.factories.Load(actorType)
	if !ok {
		return nil, false, fmt.Errorf("actor type %s not registered", actorType)
	}

	target = factory(actorID)
	t.table.Store(key.ConstructComposite(actorType, actorID), target)

	return target, true, nil
}

func (t *table) RegisterActorType(actorType string, factory targets.Factory) {
	t.actorTypesLock.Lock(actorType)
	defer t.actorTypesLock.Unlock(actorType)
	t.factories.Store(actorType, factory)
	t.typeUpdates.Batch(0, t.factories.Keys())
}

func (t *table) UnRegisterActorType(actorType string) error {
	t.lock.RLock()
	defer t.lock.RUnlock()
	t.actorTypesLock.Lock(actorType)
	defer t.actorTypesLock.Unlock(actorType)

	t.factories.Delete(actorType)

	var (
		wg   sync.WaitGroup
		errs = slice.New[error]()
	)

	t.table.Range(func(akey string, target targets.Interface) bool {
		atype, aid := key.ActorTypeAndIDFromKey(akey)
		if actorType == atype {
			wg.Add(1)
			go func(atype, aid string) {
				defer wg.Done()
				errs.Append(t.haltInLock(atype, aid))
			}(atype, aid)
		}

		return true
	})

	wg.Wait()

	t.typeUpdates.Batch(0, t.factories.Keys())

	return errors.Join(errs.Slice()...)
}

// HaltAll halts all actors currently in the table.
func (t *table) HaltAll() error {
	t.lock.Lock()
	defer t.lock.Unlock()

	errCh := make(chan error)
	t.table.Range(func(actorKey string, _ targets.Interface) bool {
		go func(actorKey string) {
			err := t.haltInLock(key.ActorTypeAndIDFromKey(actorKey))
			if err != nil {
				errCh <- fmt.Errorf("failed to deactivate actor '%s': %v", actorKey, err)
				return
			}
			errCh <- nil
		}(actorKey)
		return true
	})

	var errs []error
	for range t.table.Len() {
		if err := <-errCh; err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (t *table) haltInLock(actorType, actorID string) error {
	akey := key.ConstructComposite(actorType, actorID)
	log.Debugf("Halting actor '%s'", akey)

	// Remove the actor from the table
	// This will forbit more state changes
	target, ok := t.table.LoadAndDelete(akey)

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

func (t *table) SubscribeToTypeUpdates(ctx context.Context) <-chan []string {
	t.lock.Lock()
	defer t.lock.Unlock()
	ch := make(chan []string)
	t.typeUpdates.Subscribe(ctx, ch)
	return ch
}

func (t *table) drain(actorType, actorID string, target targets.Interface) {
	doDrain := t.drainRebalancedActors
	if v, ok := t.entityConfigs[actorType]; ok {
		doDrain = v.DrainRebalancedActors
	}

	if doDrain {
		target.CloseUntil(t.drainOngoingCallTimeout)
	}

	diag.DefaultMonitoring.ActorRebalanced(actorType)

	err := t.haltInLock(actorType, actorID)
	if err != nil {
		log.Errorf("Failed to deactivate actor '%s||%s': %v", actorType, actorID, err)
	}
}
