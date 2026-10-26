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
	"sync/atomic"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/broadcaster"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.table")

//nolint:interfacebloat
type Interface interface {
	io.Closer

	Types() []string
	ReminderTypes() []string
	IsActorTypeHosted(actorType string) bool
	GetOrCreate(ctx context.Context, actorType, actorID string) (targets.Interface, error)
	ActorExists(actorType, actorKey string) bool
	RegisterActorTypes(opts RegisterActorTypeOptions)
	UnRegisterActorTypes(actorTypes ...string) error
	SubscribeToTypeUpdates(ctx context.Context) (<-chan []string, []string)
	SetReminderDelivery(enabled bool, actorTypes ...string)
	SubscribeToReminderTypeUpdates(ctx context.Context) <-chan []string
	DrainActorTypes(actorTypes ...string)
	CompleteDrains(placed func(actorType string) bool)
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

	// NoReminderDelivery hosts the types without advertising them to the
	// scheduler for reminder delivery: they are placed, and their actors
	// serve calls routed to this host, but the scheduler delivers their
	// reminders only to the hosts which advertise them. A host which does not
	// advertise a type still forwards a reminder it receives for one of its
	// actors to the actor's owner. SetReminderDelivery toggles it in place.
	NoReminderDelivery bool
}

type table struct {
	factories   sync.Map
	createLocks sync.Map
	typeUpdates *broadcaster.Broadcaster[[]string]

	// noReminders holds the registered types which are not advertised for
	// reminder delivery (RegisterActorTypeOptions.NoReminderDelivery), and
	// draining those which are no longer advertised to placement but keep
	// their factories until placement moves them (DrainActorTypes). Both are
	// guarded by flagsLock, which also orders their changes with the
	// broadcasts on typeUpdates and reminderTypeUpdates.
	noReminders         map[string]struct{}
	draining            map[string]struct{}
	flagsLock           sync.Mutex
	reminderTypeUpdates *broadcaster.Broadcaster[[]string]

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
		typeUpdates:     broadcaster.New[[]string](),
		reentrancyStore: opts.ReentrancyStore,
		timers:          opts.Timers,

		noReminders:         make(map[string]struct{}),
		draining:            make(map[string]struct{}),
		reminderTypeUpdates: broadcaster.New[[]string](),
	}
	t.suspended.Store(opts.StartSuspended)
	return t
}

// Types returns the actor types advertised by this host. While hosting is
// suspended no types are advertised, even though factories remain registered.
func (t *table) Types() []string {
	t.flagsLock.Lock()
	defer t.flagsLock.Unlock()
	return t.typesLocked()
}

// typesLocked is Types with flagsLock held.
func (t *table) typesLocked() []string {
	if t.suspended.Load() {
		return nil
	}
	return t.registeredLocked()
}

// ReminderTypes returns the actor types advertised to the scheduler for
// reminder delivery: the advertised types, less those registered with
// NoReminderDelivery.
func (t *table) ReminderTypes() []string {
	t.flagsLock.Lock()
	defer t.flagsLock.Unlock()
	return t.reminderTypesLocked()
}

// reminderTypesLocked is ReminderTypes with flagsLock held.
func (t *table) reminderTypesLocked() []string {
	types := t.typesLocked()
	keys := types[:0]
	for _, actorType := range types {
		if _, ok := t.noReminders[actorType]; !ok {
			keys = append(keys, actorType)
		}
	}
	return keys
}

// registeredLocked returns the registered actor types regardless of
// suspension, less the draining ones. Must be called with flagsLock held.
func (t *table) registeredLocked() []string {
	var keys []string
	t.factories.Range(func(key, _ any) bool {
		if _, ok := t.draining[key.(string)]; !ok {
			keys = append(keys, key.(string))
		}
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

	return factory.(targets.Factory).GetOrCreate(actorID), nil
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

	// A type registered again while it drains first gives up the actors of
	// its draining factory, so one actor ID is never live in two factories.
	t.flagsLock.Lock()
	var drained []string
	for _, opt := range opts.Factories {
		if _, ok := t.draining[opt.Type]; ok {
			drained = append(drained, opt.Type)
		}
	}
	old := t.detachLocked(drained)
	t.flagsLock.Unlock()
	if err := t.haltDetached(old); err != nil {
		log.Warnf("Failed to halt the actors of drained actor types: %s", err)
	}

	t.flagsLock.Lock()
	typesBefore, reminderTypesBefore := t.typesLocked(), t.reminderTypesLocked()
	for _, opt := range opts.Factories {
		t.reentrancyStore.Store(opt.Type, opt.Reentrancy)
		t.createLocks.LoadOrStore(opt.Type, new(sync.RWMutex))
		if opts.NoReminderDelivery {
			t.noReminders[opt.Type] = struct{}{}
		} else {
			delete(t.noReminders, opt.Type)
		}
		t.factories.Store(opt.Type, opt.Factory)
	}
	types, reminderTypes := t.typesLocked(), t.reminderTypesLocked()
	// Registering again only types already advertised to placement causes
	// no placement round, so a change to their reminder delivery is sent
	// the way SetReminderDelivery sends it.
	if sameTypes(typesBefore, types) && !sameTypes(reminderTypesBefore, reminderTypes) {
		t.reminderTypeUpdates.Broadcast(reminderTypes)
	}
	// Broadcast under flagsLock, so a concurrent DrainActorTypes or
	// UnRegisterActorTypes cannot have its newer type list overtaken by this
	// one.
	t.typeUpdates.Broadcast(types)
	t.flagsLock.Unlock()
}

// sameTypes returns whether a and b hold the same actor types, in any order.
func sameTypes(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// SetReminderDelivery advertises the given registered types to the scheduler
// for reminder delivery, or stops advertising them, without changing their
// placement: the types stay hosted, and the actors they host keep serving.
// Unregistered types are ignored. The new reminder types are broadcast to
// SubscribeToReminderTypeUpdates subscribers if they changed.
func (t *table) SetReminderDelivery(enabled bool, actorTypes ...string) {
	t.flagsLock.Lock()
	defer t.flagsLock.Unlock()

	var changed bool
	for _, actorType := range actorTypes {
		if _, ok := t.factories.Load(actorType); !ok {
			continue
		}
		_, flagged := t.noReminders[actorType]
		if enabled == flagged {
			changed = true
		}
		if enabled {
			delete(t.noReminders, actorType)
		} else {
			t.noReminders[actorType] = struct{}{}
		}
	}

	if changed {
		t.reminderTypeUpdates.Broadcast(t.reminderTypesLocked())
	}
}

// DrainActorTypes unregisters the given registered types gracefully: they
// stop being advertised to placement at once, but keep their factories, so
// the calls the placement tables still route to this host are served rather
// than failed with the type not registered. CompleteDrains unregisters each
// once a dissemination round no longer places it here. Registering a type
// again while it drains replaces its draining factory.
func (t *table) DrainActorTypes(actorTypes ...string) {
	t.flagsLock.Lock()
	defer t.flagsLock.Unlock()

	var changed bool
	for _, actorType := range actorTypes {
		if _, ok := t.factories.Load(actorType); !ok {
			continue
		}
		if _, ok := t.draining[actorType]; !ok {
			t.draining[actorType] = struct{}{}
			changed = true
		}
	}

	if changed {
		t.typeUpdates.Broadcast(t.typesLocked())
	}
}

// CompleteDrains unregisters the draining types placed reports this host no
// longer hosts. Called by the placement disseminator at the end of each
// dissemination round, when the tables are final. HaltNonHosted already
// halted their actors when the round's tables moved them, so halting the
// factories here only halts the actors created since.
func (t *table) CompleteDrains(placed func(actorType string) bool) {
	t.flagsLock.Lock()
	var done []string
	for actorType := range t.draining {
		if !placed(actorType) {
			done = append(done, actorType)
		}
	}
	old := t.detachLocked(done)
	t.flagsLock.Unlock()

	if err := t.haltDetached(old); err != nil {
		log.Warnf("Failed to halt the actors of drained actor types: %s", err)
	}
}

// detachLocked removes the given types from the table and returns their
// factories, to be halted with haltDetached. Must be called with flagsLock
// held.
func (t *table) detachLocked(actorTypes []string) map[string]targets.Factory {
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

	detached := make(map[string]targets.Factory, len(actorTypes))
	for _, actorType := range actorTypes {
		delete(t.draining, actorType)
		delete(t.noReminders, actorType)
		t.reentrancyStore.Delete(actorType)
		if fact, ok := t.factories.LoadAndDelete(actorType); ok {
			detached[actorType] = fact.(targets.Factory)
		}
	}
	return detached
}

// haltDetached halts the factories detachLocked removed.
func (t *table) haltDetached(detached map[string]targets.Factory) error {
	errs := slice.New[error]()
	var wg sync.WaitGroup
	for actorType, fact := range detached {
		wg.Go(func() {
			t.waitCreates(actorType)
			if err := fact.HaltAll(context.Background()); err != nil {
				errs.Append(fmt.Errorf("actor type %s: %w", actorType, err))
			}
		})
	}
	wg.Wait()
	return errors.Join(errs.Slice()...)
}

// SubscribeToReminderTypeUpdates sends the reminder types (see ReminderTypes)
// whenever SetReminderDelivery changes them, or registering types again
// changes them without changing the types advertised to placement. Changes
// made by registering or unregistering types reach the scheduler with the
// placement round they cause, so they are not sent here.
func (t *table) SubscribeToReminderTypeUpdates(ctx context.Context) <-chan []string {
	ch := make(chan []string)
	t.reminderTypeUpdates.Subscribe(ctx, ch)
	return ch
}

func (t *table) UnRegisterActorTypes(actorTypes ...string) error {
	if len(actorTypes) == 0 {
		return nil
	}

	// Under flagsLock, so a concurrent SetReminderDelivery or
	// DrainActorTypes cannot flag a type after its flags are cleared here.
	t.flagsLock.Lock()
	old := t.detachLocked(actorTypes)
	t.flagsLock.Unlock()

	err := t.haltDetached(old)

	t.flagsLock.Lock()
	t.typeUpdates.Broadcast(t.typesLocked())
	t.flagsLock.Unlock()

	return err
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

	t.flagsLock.Lock()
	t.typeUpdates.Broadcast(t.typesLocked())
	t.flagsLock.Unlock()
}

func (t *table) Close() error {
	t.typeUpdates.Close()
	t.reminderTypeUpdates.Close()
	return nil
}
