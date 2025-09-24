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

package orchestrator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/lock"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/kit/concurrency/slice"
)

var orchestratorCache = sync.Pool{
	New: func() any {
		return &orchestrator{
			lock: lock.New(),
		}
	},
}

type Options struct {
	AppID             string
	WorkflowActorType string
	ActivityActorType string
	ReminderInterval  *time.Duration

	Resiliency         resiliency.Provider
	Actors             actors.Interface
	Scheduler          todo.WorkflowScheduler
	SchedulerReminders bool
	EventSink          EventSink
	ActorTypeBuilder   *common.ActorTypeBuilder
}

type factory struct {
	appID             string
	actorType         string
	activityActorType string

	resiliency       resiliency.Provider
	router           router.Interface
	reminders        reminders.Interface
	actorState       state.Interface
	placement        placement.Interface
	eventSink        EventSink
	actorTypeBuilder *common.ActorTypeBuilder

	reminderInterval   time.Duration
	schedulerReminders bool
	scheduler          todo.WorkflowScheduler

	deactivateCh chan *orchestrator

	table sync.Map
	lock  sync.Mutex
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	astate, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}

	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	reminders, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}

	placement, err := opts.Actors.Placement(ctx)
	if err != nil {
		return nil, err
	}

	reminderInterval := time.Minute * 1

	if opts.ReminderInterval != nil {
		reminderInterval = *opts.ReminderInterval
	}

	deactivateCh := make(chan *orchestrator, 100)
	go func() {
		for orchestrator := range deactivateCh {
			orchestrator.Deactivate(ctx)
		}
	}()

	return &factory{
		appID:              opts.AppID,
		actorType:          opts.WorkflowActorType,
		activityActorType:  opts.ActivityActorType,
		resiliency:         opts.Resiliency,
		router:             router,
		reminders:          reminders,
		actorState:         astate,
		reminderInterval:   reminderInterval,
		schedulerReminders: opts.SchedulerReminders,
		eventSink:          opts.EventSink,
		actorTypeBuilder:   opts.ActorTypeBuilder,
		placement:          placement,
		scheduler:          opts.Scheduler,
		deactivateCh:       deactivateCh,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	o, ok := f.table.Load(actorID)
	if !ok {
		newO := f.initOrchestrator(orchestratorCache.Get(), actorID)
		var loaded bool
		o, loaded = f.table.LoadOrStore(actorID, newO)
		if loaded {
			orchestratorCache.Put(newO)
		}
	}

	return o.(*orchestrator)
}

func (f *factory) initOrchestrator(o any, actorID string) *orchestrator {
	or := o.(*orchestrator)

	or.factory = f
	or.actorID = actorID
	or.closed.Store(false)
	or.lock.Init()

	or.state = nil
	or.rstate = nil
	or.ometa = nil
	if or.streamFns == nil {
		or.streamFns = make(map[int64]*streamFn)
	}

	// Reset the cache state to force a reload from the state store
	or.state = nil
	or.rstate = nil
	or.ometa = nil

	return or
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	var wg sync.WaitGroup
	errs := slice.New[error]()

	f.table.Range(func(_, o any) bool {
		wg.Add(1)
		go func(o *orchestrator) {
			defer wg.Done()
			errs.Append(o.Deactivate(ctx))
		}(o.(*orchestrator))
		return true
	})

	wg.Wait()

	return errors.Join(errs.Slice()...)
}

func (f *factory) HaltNonHosted(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	var wg sync.WaitGroup
	errs := slice.New[error]()

	f.table.Range(func(key, o any) bool {
		oo := o.(*orchestrator)
		if f.placement.IsActorHosted(ctx, oo.actorType, oo.actorID) {
			return true
		}

		wg.Add(1)
		go func(o *orchestrator) {
			defer wg.Done()
			errs.Append(o.Deactivate(ctx))
		}(oo)
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

func (f *factory) deactivate(orchestrator *orchestrator) {
	if !orchestrator.closed.CompareAndSwap(false, true) {
		return
	}
	f.deactivateCh <- orchestrator
}
