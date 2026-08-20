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

var ErrTimerActorNotOwned = errors.New("operations on actor timers are only possible on the host that owns the actor")

type Interface interface {
	Create(ctx context.Context, req *api.CreateTimerRequest) error
	Delete(ctx context.Context, req *api.DeleteTimerRequest) error
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

	if err := t.checkLocal(ctx, req.ActorType, req.ActorID); err != nil {
		return err
	}

	reminder, err := req.NewReminder(t.clock.Now())
	if err != nil {
		return err
	}

	reminder.IsTimer = true

	return t.storage.Create(ctx, reminder)
}

func (t *timers) Delete(ctx context.Context, req *api.DeleteTimerRequest) error {
	if err := t.checkLocal(ctx, req.ActorType, req.ActorID); err != nil {
		return err
	}

	t.storage.Delete(ctx, req.Key())
	return nil
}

// Ownership can move between this check and the storage operation; the timer
// sweep on ownership loss bounds how long such a timer can outlive it.
func (t *timers) checkLocal(ctx context.Context, actorType, actorID string) error {
	lar, _, cancel, err := t.placement.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})
	if err != nil {
		return err
	}
	cancel(nil)

	if !lar.Local {
		return ErrTimerActorNotOwned
	}

	return nil
}
