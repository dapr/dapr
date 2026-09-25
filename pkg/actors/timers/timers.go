/*
Copyright 2023 The Dapr Authors
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

package timers

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/table"
)

var (
	ErrTimerActorNotOwned      = errors.New("operations on actor timers are only possible on the host that owns the actor")
	ErrTimerActorTypeNotHosted = errors.New("operations on actor timers are only possible on hosted actor types")
)

type Interface interface {
	Create(ctx context.Context, req *api.CreateTimerRequest) error
	Delete(ctx context.Context, req *api.DeleteTimerRequest) error
	// Get returns the timer registered on this host, or nil if it does not exist.
	Get(ctx context.Context, req *api.GetTimerRequest) (*api.Reminder, error)
	// List returns the timers registered on this host for the given actor.
	List(ctx context.Context, req *api.ListTimersRequest) ([]*api.Reminder, error)
}

type Options struct {
	Storage   internaltimers.Storage
	Table     table.Interface
	Placement placement.Interface
}

// Implements a timers provider.
type timers struct {
	storage   internaltimers.Storage
	table     table.Interface
	placement placement.Interface
	clock     clock.Clock
}

func New(opts Options) Interface {
	return &timers{
		storage:   opts.Storage,
		table:     opts.Table,
		placement: opts.Placement,
		clock:     clock.RealClock{},
	}
}

func (t *timers) Create(ctx context.Context, req *api.CreateTimerRequest) error {
	if !t.table.IsActorTypeHosted(req.ActorType) {
		return fmt.Errorf("can't create timer for actor %s: actor type not registered", req.ActorKey())
	}

	reminder, err := req.NewReminder(t.clock.Now())
	if err != nil {
		return err
	}

	reminder.IsTimer = true

	cctx, cancel, err := t.claimLocal(ctx, req.ActorType, req.ActorID)
	if err != nil {
		return err
	}
	defer cancel(nil)

	return t.storage.Create(cctx, reminder)
}

func (t *timers) Delete(ctx context.Context, req *api.DeleteTimerRequest) error {
	cctx, cancel, err := t.claimLocal(ctx, req.ActorType, req.ActorID)
	if err != nil {
		return err
	}
	defer cancel(nil)

	t.storage.Delete(cctx, req.Key())
	return nil
}

func (t *timers) Get(ctx context.Context, req *api.GetTimerRequest) (*api.Reminder, error) {
	if !t.table.IsActorTypeHosted(req.ActorType) {
		return nil, ErrTimerActorTypeNotHosted
	}

	cctx, cancel, err := t.claimLocal(ctx, req.ActorType, req.ActorID)
	if err != nil {
		return nil, err
	}
	defer cancel(nil)

	return t.storage.Get(cctx, req.Key()), nil
}

func (t *timers) List(ctx context.Context, req *api.ListTimersRequest) ([]*api.Reminder, error) {
	if !t.table.IsActorTypeHosted(req.ActorType) {
		return nil, ErrTimerActorTypeNotHosted
	}

	cctx, cancel, err := t.claimLocal(ctx, req.ActorType, req.ActorID)
	if err != nil {
		return nil, err
	}
	defer cancel(nil)

	return t.storage.List(cctx, req.ActorType, req.ActorID), nil
}

// claimLocal holds the placement claim until released by the caller:
// dissemination drains in-flight claims before sweeping timers, so the
// ownership answer stays valid through the storage operation.
func (t *timers) claimLocal(ctx context.Context, actorType, actorID string) (context.Context, context.CancelCauseFunc, error) {
	lar, cctx, cancel, err := t.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})
	if err != nil {
		return nil, nil, err
	}

	if !lar.Local {
		cancel(nil)
		return nil, nil, ErrTimerActorNotOwned
	}

	return cctx, cancel, nil
}
