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
	SubscribeToReminderTypeUpdates(ctx context.Context) <-chan struct{}
	DrainActorTypes(actorTypes ...string)
	CompleteDrains(ctx context.Context, placed func(actorType string) bool)
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
	factories sync.Map
	// typeLocks holds the *typeLocks of every type ever registered.
	typeLocks   sync.Map
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
	reminderTypeUpdates *broadcaster.Broadcaster[struct{}]

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

// typeLocks are the locks of an actor type, kept across its registrations.
type typeLocks struct {
	// create is read locked by GetOrCreate while it creates an actor, and
	// write locked to wait out the creates in progress. It is never held
	// across a factory halt: a turn of an actor being halted may create an
	// actor of the same type (an orchestrator starting a child workflow),
	// and the halt waits for that turn.
	create sync.RWMutex

	// retiring holds the factories of the type the table detached and still
	// halts. While it is not empty, route serializes the creates of the type,
	// and an actor ID one of them holds is created there rather than in the
	// registered factory, so one actor ID is never live in two factories.
	// retiring only changes with both create write locked and route held, so
	// a create, which holds create read locked, reads its length without
	// route.
	route    sync.Mutex
	retiring []*retiree
}

// retiree is a factory detached from the table whose actors are halting.
// done is closed once they are.
type retiree struct {
	actorType string
	factory   targets.Factory
	done      chan struct{}
}

func New(opts Options) Interface {
	// The wfengine tests build a table but cannot import the internal
	// reentrancy store package.
	if opts.ReentrancyStore == nil {
		opts.ReentrancyStore = reentrancystore.New()
	}

	t := &table{
		entityConfigs:   make(map[string]api.EntityConfig),
		clock:           clock.RealClock{},
		typeUpdates:     broadcaster.New[[]string](),
		reentrancyStore: opts.ReentrancyStore,
		timers:          opts.Timers,

		noReminders:         make(map[string]struct{}),
		draining:            make(map[string]struct{}),
		reminderTypeUpdates: broadcaster.New[struct{}](),
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
	if v.(targets.Factory).Exists(actorID) {
		return true
	}
	if l, ok := t.typeLocks.Load(actorType); ok {
		return l.(*typeLocks).retiringFactory(actorID) != nil
	}
	return false
}

// GetOrCreate checks ctx under the type's create lock, which halts wait on.
func (t *table) GetOrCreate(ctx context.Context, actorType, actorID string) (targets.Interface, error) {
	l, ok := t.typeLocks.Load(actorType)
	if !ok {
		return nil, fmt.Errorf("%w: actor type %s not registered", actorerrors.ErrCreatingActor, actorType)
	}
	tl := l.(*typeLocks)
	tl.create.RLock()
	defer tl.create.RUnlock()

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

	return tl.getOrCreate(factory.(targets.Factory), actorID), nil
}

func (t *table) waitCreates(actorType string) {
	if l, ok := t.typeLocks.Load(actorType); ok {
		l.(*typeLocks).create.Lock()
		l.(*typeLocks).create.Unlock() //nolint:staticcheck // SA2001: waits out the creates in progress
	}
}

// getOrCreate creates actorID in factory, or returns it from a retiring
// factory which still holds it. Must be called with create read locked.
func (tl *typeLocks) getOrCreate(factory targets.Factory, actorID string) targets.Interface {
	if len(tl.retiring) == 0 {
		return factory.GetOrCreate(actorID)
	}
	tl.route.Lock()
	defer tl.route.Unlock()
	if old := tl.retiringLocked(actorID); old != nil {
		return old.GetOrCreate(actorID)
	}
	return factory.GetOrCreate(actorID)
}

// retiringFactory returns the retiring factory which holds actorID, if any.
func (tl *typeLocks) retiringFactory(actorID string) targets.Factory {
	tl.route.Lock()
	defer tl.route.Unlock()
	return tl.retiringLocked(actorID)
}

// retiringLocked is retiringFactory with route held.
func (tl *typeLocks) retiringLocked(actorID string) targets.Factory {
	for _, r := range tl.retiring {
		if r.factory.Exists(actorID) {
			return r.factory
		}
	}
	return nil
}

// halting returns the done channels of the type's retiring factories.
func (tl *typeLocks) halting() []<-chan struct{} {
	tl.route.Lock()
	defer tl.route.Unlock()
	done := make([]<-chan struct{}, 0, len(tl.retiring))
	for _, r := range tl.retiring {
		done = append(done, r.done)
	}
	return done
}

// RegisterActorTypes registers the given types. A type registered again,
// draining or not, replaces its factory at once, so it is never reported
// unregistered, and the replaced factory is halted before this returns.
// Until then the replaced factory serves the actor IDs it still holds, so
// one actor ID is never live in two factories. A halt of one of the types
// already in progress (CompleteDrains, UnRegisterActorTypes) is waited for
// too.
func (t *table) RegisterActorTypes(opts RegisterActorTypeOptions) {
	if len(opts.Factories) == 0 {
		return
	}

	if opts := opts.HostOptions; opts != nil {
		t.entityConfigs = opts.EntityConfigs
	}

	t.flagsLock.Lock()
	var (
		reminderChanged, newTypes bool
		replaced                  []*retiree
		halting                   []<-chan struct{}
	)
	for _, opt := range opts.Factories {
		_, loaded := t.factories.Load(opt.Type)
		_, draining := t.draining[opt.Type]
		_, noReminder := t.noReminders[opt.Type]
		// A draining type is no longer advertised: registering it again
		// causes a placement round, like a new type.
		registered := loaded && !draining
		newTypes = newTypes || !registered
		reminderChanged = reminderChanged || (registered && noReminder != opts.NoReminderDelivery)

		l, _ := t.typeLocks.LoadOrStore(opt.Type, new(typeLocks))
		tl := l.(*typeLocks)
		t.reentrancyStore.Store(opt.Type, opt.Reentrancy)
		if opts.NoReminderDelivery {
			t.noReminders[opt.Type] = struct{}{}
		} else {
			delete(t.noReminders, opt.Type)
		}
		delete(t.draining, opt.Type)
		if loaded {
			t.deleteTimersFunc(context.Background(), func(actorType, _ string) bool {
				return actorType == opt.Type
			})
		}
		if r := t.swapLocked(tl, opt.Type, opt.Factory); r != nil {
			replaced = append(replaced, r)
		}
		halting = append(halting, tl.halting()...)
	}
	// Registering again only types already advertised to placement causes
	// no placement round, so a change to their reminder delivery is sent
	// the way SetReminderDelivery sends it.
	if reminderChanged && !newTypes {
		t.reminderTypeUpdates.Broadcast(struct{}{})
	}
	// Broadcast under flagsLock, so a concurrent DrainActorTypes or
	// UnRegisterActorTypes cannot have its newer type list overtaken by this
	// one.
	t.typeUpdates.Broadcast(t.typesLocked())
	t.flagsLock.Unlock()

	if err := t.halt(context.Background(), replaced); err != nil {
		log.Warnf("Failed to halt the actors of replaced actor types: %s", err)
	}
	for _, done := range halting {
		<-done
	}
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
		t.reminderTypeUpdates.Broadcast(struct{}{})
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
func (t *table) CompleteDrains(ctx context.Context, placed func(actorType string) bool) {
	t.flagsLock.Lock()
	var done []string
	for actorType := range t.draining {
		if !placed(actorType) {
			done = append(done, actorType)
		}
	}
	old := t.detachLocked(done)
	t.flagsLock.Unlock()

	if err := t.halt(ctx, old); err != nil {
		log.Warnf("Failed to halt the actors of drained actor types: %s", err)
	}
}

// detachLocked removes the given types from the table and returns their
// factories, to be halted with halt. Must be called with flagsLock held.
func (t *table) detachLocked(actorTypes []string) []*retiree {
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

	var detached []*retiree
	for _, actorType := range actorTypes {
		delete(t.draining, actorType)
		delete(t.noReminders, actorType)
		t.reentrancyStore.Delete(actorType)
		if l, ok := t.typeLocks.Load(actorType); ok {
			if r := t.swapLocked(l.(*typeLocks), actorType, nil); r != nil {
				detached = append(detached, r)
			}
		}
	}
	return detached
}

// swapLocked replaces the factory of actorType with factory, or removes it
// if factory is nil, and returns the replaced factory as retiring, to be
// halted with halt. It waits out the creates in progress, so none of them
// creates an actor in the replaced factory once the type's creates are
// routed past it. Must be called with flagsLock held.
func (t *table) swapLocked(tl *typeLocks, actorType string, factory targets.Factory) *retiree {
	tl.create.Lock()
	defer tl.create.Unlock()

	var (
		old    any
		loaded bool
	)
	if factory == nil {
		old, loaded = t.factories.LoadAndDelete(actorType)
	} else {
		old, loaded = t.factories.Swap(actorType, factory)
	}
	if !loaded {
		return nil
	}

	r := &retiree{actorType: actorType, factory: old.(targets.Factory), done: make(chan struct{})}
	tl.route.Lock()
	tl.retiring = append(tl.retiring, r)
	tl.route.Unlock()
	return r
}

// halt halts the retiring factories swapLocked returned, and retires them.
func (t *table) halt(ctx context.Context, retiring []*retiree) error {
	errs := slice.New[error]()
	var wg sync.WaitGroup
	for _, r := range retiring {
		l, _ := t.typeLocks.Load(r.actorType)
		wg.Go(func() {
			if err := l.(*typeLocks).halt(ctx, r); err != nil {
				errs.Append(fmt.Errorf("actor type %s: %w", r.actorType, err))
			}
		})
	}
	wg.Wait()
	return errors.Join(errs.Slice()...)
}

// halt halts the actors of the retiring factory r, then removes it from the
// type. While the type is registered again, r keeps serving the actor IDs it
// holds, and may create one again after its halt deactivated it, so r is
// halted until it holds no actors, or a halt fails. Each re-creation needs a
// call to race the halt of its actor, so this does not spin for long. The
// create lock is only taken between halts, to remove r with no create in
// progress. Once removed, r is retired (targets.Retirer) before done is
// closed, whether or not its halt succeeded: nothing routes to it any more.
func (tl *typeLocks) halt(ctx context.Context, r *retiree) error {
	defer close(r.done)

	tl.create.Lock()
	tl.create.Unlock() //nolint:staticcheck // SA2001: waits out the creates in progress

	for {
		err := r.factory.HaltAll(ctx)

		tl.create.Lock()
		left := r.factory.Len()
		if err != nil || ctx.Err() != nil || left == 0 {
			tl.route.Lock()
			tl.retiring = slices.DeleteFunc(tl.retiring, func(o *retiree) bool { return o == r })
			tl.route.Unlock()
			tl.create.Unlock()
			if f, ok := r.factory.(targets.Retirer); ok {
				f.Retire()
			}
			if err == nil && left > 0 {
				err = fmt.Errorf("%d actors left after halting", left)
			}
			return err
		}
		tl.create.Unlock()
	}
}

// SubscribeToReminderTypeUpdates signals whenever SetReminderDelivery changes
// the reminder types (see ReminderTypes), or registering types again changes
// them without changing the types advertised to placement. Changes made by
// registering or unregistering types reach the scheduler with the placement
// round they cause, so they are not signalled here.
func (t *table) SubscribeToReminderTypeUpdates(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{})
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

	err := t.halt(context.Background(), old)

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
	// Under flagsLock, like ResumeHosting, so a concurrent registration
	// cannot broadcast its type list after the empty one.
	t.flagsLock.Lock()
	if !t.suspended.CompareAndSwap(false, true) {
		t.flagsLock.Unlock()
		return nil
	}

	// De-advertise before halting so new work is routed away from this host
	// while instances drain.
	t.typeUpdates.Broadcast(nil)
	t.flagsLock.Unlock()

	return t.HaltAll(ctx)
}

// ResumeHosting re-advertises all registered actor types after a
// SuspendHosting. Called when an actor state store becomes configured at
// runtime.
func (t *table) ResumeHosting() {
	t.flagsLock.Lock()
	defer t.flagsLock.Unlock()
	if !t.suspended.CompareAndSwap(true, false) {
		return
	}

	t.typeUpdates.Broadcast(t.typesLocked())
}

func (t *table) Close() error {
	t.typeUpdates.Close()
	t.reminderTypeUpdates.Close()
	return nil
}
