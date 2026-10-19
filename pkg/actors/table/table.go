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
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/broadcaster"
)

//nolint:interfacebloat
type Interface interface {
	io.Closer

	Types() []string
	StandbyTypes() []string
	IsActorTypeHosted(actorType string) bool
	GetOrCreate(ctx context.Context, actorType, actorID string) (targets.Interface, error)
	ActorExists(actorType, actorKey string) bool
	RegisterActorTypes(opts RegisterActorTypeOptions)
	UnRegisterActorTypes(actorTypes ...string) error
	ReplaceActorTypes(opts RegisterActorTypeOptions, remove ...string) error
	SubscribeToTypeUpdates(ctx context.Context) (<-chan HostedTypes, HostedTypes)
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

	// Timers is a closure because the timer storage is constructed after the
	// table.
	Timers func() internaltimers.Storage
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

	// Standby registers the factories' types as standby: they are reported to
	// placement apart from the active types, and placement routes a standby
	// type to this host only while no host serves it as active. Standby types
	// are hosted (instances can be created) but are not advertised to the
	// scheduler, so their reminders are not delivered to this host.
	Standby bool
}

// HostedTypes are the actor types this host advertises to placement.
type HostedTypes struct {
	Active  []string
	Standby []string
}

type table struct {
	factories   sync.Map
	createLocks sync.Map
	typeUpdates *broadcaster.Broadcaster[HostedTypes]

	// standby holds the registered types which are hosted as standby.
	standby sync.Map

	// drains holds, per type, the factory ReplaceActorTypes replaced while it
	// is being halted.
	drains sync.Map

	entityConfigs map[string]api.EntityConfig

	reentrancyStore *reentrancystore.Store
	clock           clock.Clock
	timers          func() internaltimers.Storage

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
		typeUpdates:     broadcaster.New[HostedTypes](),
		reentrancyStore: opts.ReentrancyStore,
		timers:          opts.Timers,
	}
	t.suspended.Store(opts.StartSuspended)
	return t
}

// Types returns the active actor types advertised by this host. While
// hosting is suspended no types are advertised, even though factories remain
// registered. Standby types are not included.
func (t *table) Types() []string {
	if t.suspended.Load() {
		return nil
	}
	return t.types(false)
}

// StandbyTypes returns the standby actor types advertised by this host. While
// hosting is suspended no types are advertised.
func (t *table) StandbyTypes() []string {
	if t.suspended.Load() {
		return nil
	}
	return t.types(true)
}

// types returns the registered active or standby actor types regardless of
// suspension.
func (t *table) types(standby bool) []string {
	var keys []string
	t.factories.Range(func(key, _ any) bool {
		if _, ok := t.standby.Load(key); ok == standby {
			keys = append(keys, key.(string))
		}
		return true
	})
	return keys
}

// hostedTypes returns the active and standby types to advertise, from a
// single pass over the factories so a type is never in both or neither.
func (t *table) hostedTypes() HostedTypes {
	var ht HostedTypes
	if t.suspended.Load() {
		return ht
	}
	t.factories.Range(func(key, _ any) bool {
		if _, ok := t.standby.Load(key); ok {
			ht.Standby = append(ht.Standby, key.(string))
		} else {
			ht.Active = append(ht.Active, key.(string))
		}
		return true
	})
	return ht
}

func (t *table) Len() map[string]int {
	alen := make(map[string]int)

	t.factories.Range(func(key, factory any) bool {
		alen[key.(string)] = factory.(targets.Factory).Len()
		return true
	})

	return alen
}

// deleteTimersFunc is called before actors are halted so a timer can never
// fire between its actor's halt and its own removal.
func (t *table) deleteTimersFunc(ctx context.Context, fn func(actorType, actorID string) bool) {
	if t.timers == nil {
		return
	}
	if storage := t.timers(); storage != nil {
		storage.DeleteFunc(ctx, fn)
	}
}

func (t *table) HaltAll(ctx context.Context) error {
	t.deleteTimersFunc(ctx, func(string, string) bool { return true })

	var wg sync.WaitGroup
	errs := slice.New[error]()
	t.factories.Range(func(key, factory any) bool {
		wg.Add(1)
		go func(actorType string, factory targets.Factory) {
			defer wg.Done()
			t.waitCreates(actorType)
			errs.Append(factory.HaltAll(ctx))
		}(key.(string), factory.(targets.Factory))

		return true
	})
	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (t *table) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	t.deleteTimersFunc(ctx, func(actorType, actorID string) bool {
		return !fn(&api.LookupActorRequest{ActorType: actorType, ActorID: actorID})
	})

	var wg sync.WaitGroup
	errs := slice.New[error]()
	t.factories.Range(func(key, factory any) bool {
		wg.Add(1)
		go func(actorType string, factory targets.Factory) {
			defer wg.Done()
			t.waitCreates(actorType)
			errs.Append(factory.HaltNonHosted(ctx, fn))
		}(key.(string), factory.(targets.Factory))
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

// GetOrCreate checks ctx under the type's create lock, which halts wait on.
func (t *table) GetOrCreate(ctx context.Context, actorType, actorID string) (targets.Interface, error) {
	l, ok := t.createLocks.Load(actorType)
	if !ok {
		return nil, fmt.Errorf("%w: actor type %s not registered", actorerrors.ErrCreatingActor, actorType)
	}
	l.(*sync.RWMutex).RLock()
	defer l.(*sync.RWMutex).RUnlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if t.suspended.Load() {
		return nil, fmt.Errorf("%w: actor hosting is suspended: no actor state store configured", actorerrors.ErrCreatingActor)
	}

	factory, ok := t.factories.Load(actorType)
	if !ok {
		return nil, fmt.Errorf("%w: actor type %s not registered", actorerrors.ErrCreatingActor, actorType)
	}

	if d, ok := t.drains.Load(actorType); ok {
		if err := d.(*drain).wait(ctx, actorID); err != nil {
			return nil, err
		}
	}

	return factory.(targets.Factory).GetOrCreate(actorID), nil
}

// drainPollInterval is how often a GetOrCreate waiting on a drain checks
// whether the replaced factory still holds its actor.
const drainPollInterval = 10 * time.Millisecond

// drain is a factory ReplaceActorTypes replaced, while it is being halted.
type drain struct {
	factory targets.Factory
	done    chan struct{}
}

// wait returns once the drained factory no longer holds actorID, or is
// halted.
func (d *drain) wait(ctx context.Context, actorID string) error {
	if !d.factory.Exists(actorID) {
		return nil
	}

	ticker := time.NewTicker(drainPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !d.factory.Exists(actorID) {
				return nil
			}
		}
	}
}

func (t *table) waitCreates(actorType string) {
	if l, ok := t.createLocks.Load(actorType); ok {
		l.(*sync.RWMutex).Lock()
		l.(*sync.RWMutex).Unlock() //nolint:staticcheck // SA2001: waits out the creates in progress
	}
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
		t.createLocks.LoadOrStore(opt.Type, new(sync.RWMutex))
		if opts.Standby {
			t.standby.Store(opt.Type, struct{}{})
		} else {
			t.standby.Delete(opt.Type)
		}
		t.factories.Store(opt.Type, opt.Factory)
	}

	t.typeUpdates.Broadcast(t.hostedTypes())
}

func (t *table) UnRegisterActorTypes(actorTypes ...string) error {
	if len(actorTypes) == 0 {
		return nil
	}

	removed := make(map[string]struct{}, len(actorTypes))
	for _, actorType := range actorTypes {
		removed[actorType] = struct{}{}
	}
	t.deleteTimersFunc(context.Background(), func(actorType, _ string) bool {
		_, ok := removed[actorType]
		return ok
	})

	errs := slice.New[error]()
	var wg sync.WaitGroup
	for _, actorType := range actorTypes {
		t.reentrancyStore.Delete(actorType)

		fact, ok := t.factories.LoadAndDelete(actorType)
		t.standby.Delete(actorType)
		if !ok {
			continue
		}

		wg.Add(1)

		go func(fact targets.Factory) {
			t.waitCreates(actorType)
			errs.Append(fact.HaltAll(context.Background()))
			wg.Done()
		}(fact.(targets.Factory))
	}

	wg.Wait()

	t.typeUpdates.Broadcast(t.hostedTypes())

	return errors.Join(errs.Slice()...)
}

// ReplaceActorTypes registers opts' factories over the factories registered
// for their types and unregisters the remove types, reporting the change to
// placement once, so a type moving between active and standby stays hosted
// throughout. It then halts the replaced and removed factories, waiting out
// their turns in flight. Until a replaced factory has halted an actor, a
// GetOrCreate of that actor waits, so the actor's old and new instances never
// run at once. Callers serialize it with the other registrations.
func (t *table) ReplaceActorTypes(opts RegisterActorTypeOptions, remove ...string) error {
	types := make(map[string]struct{}, len(opts.Factories)+len(remove))
	for _, opt := range opts.Factories {
		types[opt.Type] = struct{}{}
	}
	for _, actorType := range remove {
		types[actorType] = struct{}{}
	}
	t.deleteTimersFunc(context.Background(), func(actorType, _ string) bool {
		_, ok := types[actorType]
		return ok
	})

	if opts := opts.HostOptions; opts != nil {
		t.entityConfigs = opts.EntityConfigs
	}

	var drains []*drain
	for _, opt := range opts.Factories {
		t.reentrancyStore.Store(opt.Type, opt.Reentrancy)
		l, _ := t.createLocks.LoadOrStore(opt.Type, new(sync.RWMutex))

		// Swap under the create lock: a create in progress finishes on the
		// replaced factory, whose halt below reaches its actor, and every
		// later one sees the new factory together with the drain.
		l.(*sync.RWMutex).Lock()
		if opts.Standby {
			t.standby.Store(opt.Type, struct{}{})
		} else {
			t.standby.Delete(opt.Type)
		}
		if prev, ok := t.factories.Swap(opt.Type, opt.Factory); ok {
			d := &drain{factory: prev.(targets.Factory), done: make(chan struct{})}
			t.drains.Store(opt.Type, d)
			drains = append(drains, d)
		}
		l.(*sync.RWMutex).Unlock()
	}

	var removed []targets.Factory
	for _, actorType := range remove {
		t.reentrancyStore.Delete(actorType)
		fact, ok := t.factories.LoadAndDelete(actorType)
		t.standby.Delete(actorType)
		if ok {
			t.waitCreates(actorType)
			removed = append(removed, fact.(targets.Factory))
		}
	}

	t.typeUpdates.Broadcast(t.hostedTypes())

	errs := slice.New[error]()
	var wg sync.WaitGroup
	for _, d := range drains {
		wg.Go(func() {
			errs.Append(d.factory.HaltAll(context.Background()))
			close(d.done)
		})
	}
	for _, fact := range removed {
		wg.Go(func() {
			errs.Append(fact.HaltAll(context.Background()))
		})
	}
	wg.Wait()

	for _, opt := range opts.Factories {
		t.drains.Delete(opt.Type)
	}

	return errors.Join(errs.Slice()...)
}

func (t *table) SubscribeToTypeUpdates(ctx context.Context) (<-chan HostedTypes, HostedTypes) {
	ch := make(chan HostedTypes)
	t.typeUpdates.Subscribe(ctx, ch)
	return ch, t.hostedTypes()
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
	t.typeUpdates.Broadcast(HostedTypes{})

	return t.HaltAll(ctx)
}

// ResumeHosting re-advertises all registered actor types after a
// SuspendHosting. Called when an actor state store becomes configured at
// runtime.
func (t *table) ResumeHosting() {
	if !t.suspended.CompareAndSwap(true, false) {
		return
	}

	t.typeUpdates.Broadcast(t.hostedTypes())
}

func (t *table) Close() error {
	t.typeUpdates.Close()
	return nil
}
