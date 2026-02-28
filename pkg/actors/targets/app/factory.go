/*
Copyright 2025 The Dapr Authors
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

package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/app/lock"
	"github.com/dapr/dapr/pkg/channel"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/queue"
	"github.com/dapr/kit/ptr"
)

type Options struct {
	ActorType    string
	AppChannel   channel.AppChannel
	Resiliency   resiliency.Provider
	IdleTimeout  time.Duration
	clock        clock.Clock
	Reentrancy   *reentrancystore.Store
	Placement    placement.Interface
	EntityConfig *api.EntityConfig
}

type factory struct {
	actorType    string
	appChannel   channel.AppChannel
	resiliency   resiliency.Provider
	idlerQueue   *queue.Processor[string, *app]
	reentrancy   *reentrancystore.Store
	clock        clock.Clock
	placement    placement.Interface
	entityConfig *api.EntityConfig

	// idleTimeout is the configured max idle time for actors of this kind.
	idleTimeout time.Duration

	table sync.Map
	lock  sync.RWMutex
}

func New(opts Options) targets.Factory {
	if opts.clock == nil {
		opts.clock = clock.RealClock{}
	}

	f := &factory{
		actorType:    opts.ActorType,
		appChannel:   opts.AppChannel,
		resiliency:   opts.Resiliency,
		placement:    opts.Placement,
		clock:        opts.clock,
		idleTimeout:  opts.IdleTimeout,
		reentrancy:   opts.Reentrancy,
		entityConfig: opts.EntityConfig,
	}

	f.idlerQueue = queue.NewProcessor[string, *app](queue.Options[string, *app]{
		ExecuteFn: f.handleIdleActor,
		Clock:     f.clock,
	})

	return f
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	f.lock.RLock()
	defer f.lock.RUnlock()

	a, ok := f.table.Load(actorID)
	if !ok {
		newApp := f.initApp(actorID)
		a, _ = f.table.LoadOrStore(actorID, newApp)
	}

	aa := a.(*app)

	return aa
}

func (f *factory) initApp(actorID string) *app {
	app := &app{
		actorID: actorID,
		factory: f,
		clock:   f.clock,
		lock: lock.New(lock.Options{
			ActorType:   f.actorType,
			ConfigStore: f.reentrancy,
		}),
	}

	app.idleAt.Store(ptr.Of(f.clock.Now().Add(f.idleTimeout)))

	f.idlerQueue.Enqueue(app)

	return app
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.haltActors(ctx, func(actorID string) bool {
		return false
	})
}

func (f *factory) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.haltActors(ctx, func(actorID string) bool {
		return fn(&api.LookupActorRequest{
			ActorType: f.actorType,
			ActorID:   actorID,
		})
	})
}

func (f *factory) haltActors(ctx context.Context, fn func(string) bool) error {
	var wg sync.WaitGroup
	errs := slice.New[error]()

	f.table.Range(func(key, a any) bool {
		aa := a.(*app)

		if fn(aa.actorID) {
			return true
		}

		wg.Add(1)
		go func(aa *app) {
			defer wg.Done()
			errs.Append(f.halt(ctx, aa))
		}(aa)

		return true
	})

	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (f *factory) Exists(actorID string) bool {
	_, ok := f.table.Load(actorID)
	return ok
}

func (f *factory) Len() int {
	var count int
	f.table.Range(func(_, _ any) bool { count++; return true })
	return count
}

func (f *factory) handleIdleActor(target *app) {
	ctx, cancel, err := f.placement.Lock(context.Background())
	if err != nil {
		log.Errorf("Failed to lock placement for idle actor deactivation: %s", err)
		return
	}
	defer cancel(nil)

	f.lock.Lock()
	defer f.lock.Unlock()

	log.Debugf("Actor %s is idle, deactivating", target.Key())

	if err := f.halt(ctx, target); err != nil {
		log.Errorf("Failed to halt actor %s: %s", target.Key(), err)
		return
	}
}

func (f *factory) halt(ctx context.Context, app *app) error {
	key := app.Key()

	diag.DefaultMonitoring.ActorRebalanced(app.Type())

	log.Debugf("Halting actor '%s'", key)

	return app.Deactivate(ctx)
}
