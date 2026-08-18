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
	"sync/atomic"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/broadcaster"
)

//nolint:interfacebloat
type Interface interface {
	io.Closer

	Types() []string
	IsActorTypeHosted(actorType string) bool
	GetOrCreate(actorType, actorID string) (targets.Interface, error)
	ActorExists(actorType, actorKey string) bool
	RegisterActorTypes(opts RegisterActorTypeOptions)
	UnRegisterActorTypes(actorTypes ...string) error
	SubscribeToTypeUpdates(ctx context.Context) (<-chan []string, []string)
	HaltAll(ctx context.Context) error
	HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error
	Len() map[string]int
	SuspendHosting(ctx context.Context) error
	ResumeHosting()
}

type Options struct {
	ReentrancyStore *reentrancystore.Store

	// StartSuspended starts the table with hosting suspended: registered actor
	// types are not advertised and no actor instances can be created until
	// ResumeHosting is called. Used when no actor state store is configured at
	// startup.
	StartSuspended bool
}

type ActorTypeFactory struct {
	Type       string
	Reentrancy config.ReentrancyConfig
	Factory    targets.Factory
}

type ActorHostOptions struct {
	EntityConfigs map[string]api.EntityConfig
}

type RegisterActorTypeOptions struct {
	HostOptions *ActorHostOptions
	Factories   []ActorTypeFactory
}

type table struct {
	factories   sync.Map
	typeUpdates *broadcaster.Broadcaster[[]string]

	entityConfigs map[string]api.EntityConfig

	reentrancyStore *reentrancystore.Store
	clock           clock.Clock

	// suspended hides all registered actor types from advertisement and
	// blocks actor instance creation, without removing the registered
	// factories. Hosting is suspended while no actor state store is
	// configured; the store can be hot reloaded at runtime.
	suspended atomic.Bool
}

func New(opts Options) Interface {
	t := &table{
		entityConfigs:   make(map[string]api.EntityConfig),
		clock:           clock.RealClock{},
		typeUpdates:     broadcaster.New[[]string](),
		reentrancyStore: opts.ReentrancyStore,
	}
	t.suspended.Store(opts.StartSuspended)
	return t
}

// Types returns the actor types advertised by this host. While hosting is
// suspended no types are advertised, even though factories remain registered.
func (t *table) Types() []string {
	if t.suspended.Load() {
		return nil
	}
	return t.types()
}

// types returns the registered actor types regardless of suspension.
func (t *table) types() []string {
	var keys []string
	t.factories.Range(func(key, _ any) bool {
		keys = append(keys, key.(string))
		return true
	})
	return keys
}

func (t *table) Len() map[string]int {
	alen := make(map[string]int)

	t.factories.Range(func(key, factory any) bool {
		alen[key.(string)] = factory.(targets.Factory).Len()
		return true
	})

	return alen
}

func (t *table) HaltAll(ctx context.Context) error {
	var wg sync.WaitGroup
	errs := slice.New[error]()
	t.factories.Range(func(_, factory any) bool {
		wg.Add(1)
		go func(factory targets.Factory) {
			defer wg.Done()
			errs.Append(factory.HaltAll(ctx))
		}(factory.(targets.Factory))

		return true
	})
	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (t *table) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	var wg sync.WaitGroup
	errs := slice.New[error]()
	t.factories.Range(func(_, factory any) bool {
		wg.Add(1)
		go func(factory targets.Factory) {
			defer wg.Done()
			errs.Append(factory.HaltNonHosted(ctx, fn))
		}(factory.(targets.Factory))
		return true
	})
	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (t *table) IsActorTypeHosted(actorType string) bool {
	if t.suspended.Load() {
		return false
	}
	_, ok := t.factories.Load(actorType)
	return ok
}

func (t *table) ActorExists(actorType, actorID string) bool {
	v, ok := t.factories.Load(actorType)
	if !ok {
		return false
	}
	return v.(targets.Factory).Exists(actorID)
}

func (t *table) GetOrCreate(actorType, actorID string) (targets.Interface, error) {
	if t.suspended.Load() {
		return nil, fmt.Errorf("%w: actor hosting is suspended: no actor state store configured", actorerrors.ErrCreatingActor)
	}

	factory, ok := t.factories.Load(actorType)
	if !ok {
		return nil, fmt.Errorf("%w: actor type %s not registered", actorerrors.ErrCreatingActor, actorType)
	}

	return factory.(targets.Factory).GetOrCreate(actorID), nil
}

func (t *table) RegisterActorTypes(opts RegisterActorTypeOptions) {
	if len(opts.Factories) == 0 {
		return
	}

	if opts := opts.HostOptions; opts != nil {
		t.entityConfigs = opts.EntityConfigs
	}

	for _, opt := range opts.Factories {
		t.reentrancyStore.Store(opt.Type, opt.Reentrancy)
		t.factories.Store(opt.Type, opt.Factory)
	}

	t.typeUpdates.Broadcast(t.Types())
}

func (t *table) UnRegisterActorTypes(actorTypes ...string) error {
	if len(actorTypes) == 0 {
		return nil
	}

	errs := slice.New[error]()
	var wg sync.WaitGroup
	for _, actorType := range actorTypes {
		t.reentrancyStore.Delete(actorType)

		fact, ok := t.factories.LoadAndDelete(actorType)
		if !ok {
			continue
		}

		wg.Add(1)

		go func(fact targets.Factory) {
			errs.Append(fact.HaltAll(context.Background()))
			wg.Done()
		}(fact.(targets.Factory))
	}

	wg.Wait()

	t.typeUpdates.Broadcast(t.Types())

	return errors.Join(errs.Slice()...)
}

func (t *table) SubscribeToTypeUpdates(ctx context.Context) (<-chan []string, []string) {
	ch := make(chan []string)
	t.typeUpdates.Subscribe(ctx, ch)
	return ch, t.Types()
}

// SuspendHosting stops this host from hosting actors: registered types are
// de-advertised (an empty type list is broadcast to placement subscribers)
// and all currently hosted actor instances are halted, delivering
// deactivations to the app. Registered factories, reentrancy configs, and
// entity configs are retained so hosting can resume without re-registration.
// Called when the actor state store is removed at runtime.
func (t *table) SuspendHosting(ctx context.Context) error {
	if !t.suspended.CompareAndSwap(false, true) {
		return nil
	}

	// De-advertise before halting so new work is routed away from this host
	// while instances drain.
	t.typeUpdates.Broadcast(nil)

	return t.HaltAll(ctx)
}

// ResumeHosting re-advertises all registered actor types after a
// SuspendHosting. Called when an actor state store becomes configured at
// runtime.
func (t *table) ResumeHosting() {
	if !t.suspended.CompareAndSwap(true, false) {
		return
	}

	t.typeUpdates.Broadcast(t.types())
}

func (t *table) Close() error {
	t.typeUpdates.Close()
	return nil
}
