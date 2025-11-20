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

package activity

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/router"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

var activityCache = &sync.Pool{
	New: func() any {
		return &activity{
			lock: lock.New(),
		}
	},
}

type Options struct {
	AppID             string
	ActivityActorType string
	WorkflowActorType string
	Scheduler         todo.ActivityScheduler
	Actors            actors.Interface
	ActorTypeBuilder  *common.ActorTypeBuilder
}

type factory struct {
	appID             string
	actorType         string
	workflowActorType string

	router           router.Interface
	state            state.Interface
	reminders        reminders.Interface
	placement        placement.Interface
	actorTypeBuilder *common.ActorTypeBuilder

	scheduler todo.ActivityScheduler

	table sync.Map
	lock  sync.Mutex
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	router, err := opts.Actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	state, err := opts.Actors.State(ctx)
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

	return &factory{
		appID:             opts.AppID,
		actorType:         opts.ActivityActorType,
		router:            router,
		reminders:         reminders,
		scheduler:         opts.Scheduler,
		placement:         placement,
		workflowActorType: opts.WorkflowActorType,
		actorTypeBuilder:  opts.ActorTypeBuilder,
		state:             state,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	a, ok := f.table.Load(actorID)
	if !ok {
		newActivity := f.initActivity(activityCache.Get(), actorID)
		var loaded bool
		a, loaded = f.table.LoadOrStore(actorID, newActivity)
		if loaded {
			activityCache.Put(newActivity)
		}
	}

	return a.(*activity)
}

func (f *factory) initActivity(a any, actorID string) *activity {
	act := a.(*activity)

	act.factory = f
	act.actorID = actorID

	return act
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		val.(*activity).Deactivate(ctx)
		return true
	})
	f.table.Clear()
	return nil
}

func (f *factory) HaltNonHosted(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		if !f.placement.IsActorHosted(ctx, f.actorType, key.(string)) {
			val.(*activity).Deactivate(ctx)
			f.table.Delete(key)
		}
		return true
	})
	return nil
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
