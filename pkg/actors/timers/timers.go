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
	"fmt"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/table"
)

type Interface interface {
	Create(ctx context.Context, req *api.CreateTimerRequest) error
	Delete(ctx context.Context, req *api.DeleteTimerRequest)
}

type Options struct {
	Storage internaltimers.Storage
	Table   table.Interface
}

// Implements a timers provider.
type timers struct {
	storage internaltimers.Storage
	table   table.Interface
	clock   clock.Clock
}

func New(opts Options) Interface {
	return &timers{
		storage: opts.Storage,
		table:   opts.Table,
		clock:   clock.RealClock{},
	}
}

func (t *timers) Create(ctx context.Context, req *api.CreateTimerRequest) error {
	if !t.table.IsActorTypeHosted(req.ActorType) {
		return fmt.Errorf("can't create timer for actor %s: actor type not registered", req.ActorKey())
	}

	reminder, err := req.NewReminder(t.clock.Now(), false)
	if err != nil {
		return err
	}

	reminder.IsTimer = true

	return t.storage.Create(ctx, reminder)
}

func (t *timers) Delete(ctx context.Context, req *api.DeleteTimerRequest) {
	t.storage.Delete(ctx, req.Key())
}
