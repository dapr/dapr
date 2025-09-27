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

package executor

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/lock"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

var executorCache = &sync.Pool{
	New: func() any {
		return &executor{
			lock: lock.New(),
		}
	},
}

type Options struct {
	Actors actors.Interface

	ActorType string
}

type factory struct {
	actorType string

	placement    placement.Interface
	deactivateCh chan *executor

	table sync.Map
	lock  sync.Mutex
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	placement, err := opts.Actors.Placement(ctx)
	if err != nil {
		return nil, err
	}

	deactivateCh := make(chan *executor, 100)
	go func() {
		for executor := range deactivateCh {
			executor.Deactivate(ctx)
		}
	}()

	return &factory{
		actorType:    opts.ActorType,
		placement:    placement,
		deactivateCh: deactivateCh,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	a, ok := f.table.Load(actorID)
	if !ok {
		newActivity := f.initExecutor(executorCache.Get(), actorID)
		var loaded bool
		a, loaded = f.table.LoadOrStore(actorID, newActivity)
		if loaded {
			executorCache.Put(newActivity)
		}
	}

	return a.(*executor)
}

func (f *factory) initExecutor(a any, actorID string) *executor {
	act := a.(*executor)

	act.factory = f
	act.actorID = actorID

	act.closed.Store(false)
	act.completeCh = make(chan *internalsv1pb.InternalInvokeResponse, 1)
	act.cancelCh = make(chan struct{})
	act.closeCh = make(chan struct{})
	act.watchLock = make(chan struct{}, 1)

	return act
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		val.(*executor).Deactivate(ctx)
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
			val.(*executor).Deactivate(ctx)
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
