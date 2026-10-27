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
	"sync/atomic"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func newExecutor() *executor {
	return &executor{
		lock: lock.New(),
	}
}

type Options struct {
	Actors actors.Interface

	ActorType string

	// Pending is the process-local completion rendezvous shared with the
	// cluster tasks backend. Completions arriving at this actor are
	// delivered into it when the waiter is registered on this host.
	Pending *pending.Pending
}

type factory struct {
	actorType string

	actors       actors.Interface
	placement    placement.Interface
	deactivateCh chan *executor
	pending      *pending.Pending

	table sync.Map
	lock  sync.Mutex

	// dropParked has a retiring actor drop what it still holds instead of
	// handing it off; see DropParked.
	dropParked atomic.Bool
}

// ParkedDropper is implemented by the executor actor factory; see DropParked.
type ParkedDropper interface {
	DropParked()
}

// DropParked has the actors of this factory drop the completions and
// cancellations still parked on them when they retire, instead of handing
// them off to the owner of their key. A daprd turning into a fallback host
// calls it before it unregisters the type: it does so only when no daprd of
// the app has an executor, so no daprd hosts the type and a handoff could
// only fail, after retrying for its whole forwardTimeout. A dropped
// completion is redelivered by the durable reminder retry.
func (f *factory) DropParked() {
	f.dropParked.Store(true)
}

func New(ctx context.Context, opts Options) (targets.Factory, error) {
	placement, err := opts.Actors.Placement(ctx)
	if err != nil {
		return nil, err
	}

	// The worker exits with ctx: a factory is built on every registration of
	// the workflow actor types. Actors still queued stay in the table until
	// HaltAll or HaltNonHosted reaps them.
	deactivateCh := make(chan *executor, 100)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case executor := <-deactivateCh:
				executor.Deactivate(ctx)
			}
		}
	}()

	return &factory{
		actorType:    opts.ActorType,
		actors:       opts.Actors,
		placement:    placement,
		deactivateCh: deactivateCh,
		pending:      opts.Pending,
	}, nil
}

func (f *factory) GetOrCreate(actorID string) targets.Interface {
	a, ok := f.table.Load(actorID)
	if !ok {
		fresh := f.initExecutor(newExecutor(), actorID)
		a, _ = f.table.LoadOrStore(actorID, fresh)
	}

	return a.(*executor)
}

func (f *factory) initExecutor(a any, actorID string) *executor {
	act := a.(*executor)

	act.factory = f
	act.actorID = actorID

	act.closed.Store(false)
	act.cancelClosed.Store(false)
	act.completeCh = make(chan *internalsv1pb.InternalInvokeResponse, 1)
	act.slotFreed = make(chan struct{})
	act.cancelCh = make(chan struct{})
	act.closeCh = make(chan struct{})
	act.watchLock = make(chan struct{}, 1)

	return act
}

func (f *factory) HaltAll(ctx context.Context) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		val.(*executor).halt(ctx)
		return true
	})
	f.table.Clear()
	return nil
}

func (f *factory) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.table.Range(func(key, val any) bool {
		if !fn(&api.LookupActorRequest{
			ActorType: f.actorType,
			ActorID:   key.(string),
		}) {
			val.(*executor).halt(ctx)
			// Only this actor: the halt's forward can already have created
			// a fresh actor for the key when the key resolves back here.
			f.table.CompareAndDelete(key, val)
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
