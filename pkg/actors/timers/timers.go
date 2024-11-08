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

	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/kit/logger"
	"k8s.io/utils/clock"
)

var log = logger.NewLogger("dapr.runtime.actors.timers")

type Interface interface {
	Create(ctx context.Context, req *requestresponse.CreateTimerRequest) error
	Delete(ctx context.Context, req *requestresponse.DeleteTimerRequest)
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

func (t *timers) Create(ctx context.Context, req *requestresponse.CreateTimerRequest) error {
	if !t.table.IsActorTypeHosted(req.ActorType) {
		return fmt.Errorf("can't create timer for actor %s: actor type not registered", req.ActorKey())
	}

	reminder, err := req.NewReminder(t.clock.Now())
	if err != nil {
		return err
	}

	reminder.IsTimer = true

	return t.storage.Create(ctx, reminder)
}

func (t *timers) Delete(ctx context.Context, req *requestresponse.DeleteTimerRequest) {
	t.storage.Delete(ctx, req.Key())
}
