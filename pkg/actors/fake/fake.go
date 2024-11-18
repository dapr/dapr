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

package fake

import (
	"context"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/engine"
	enginefake "github.com/dapr/dapr/pkg/actors/engine/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/timers"
	timersfake "github.com/dapr/dapr/pkg/actors/timers/fake"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type Fake struct {
	fnInitFn          func(actors.InitOptions) error
	fnRunFn           func(context.Context) error
	fnEngineFn        func(context.Context) (engine.Interface, error)
	fnTableFn         func(context.Context) (table.Interface, error)
	fnStateFn         func(context.Context) (state.Interface, error)
	fnTimersFn        func(context.Context) (timers.Interface, error)
	fnRemindersFn     func(context.Context) (reminders.Interface, error)
	fnRuntimeStatusFn func() *runtimev1pb.ActorRuntime
}

func New() *Fake {
	return &Fake{
		fnInitFn: func(actors.InitOptions) error {
			return nil
		},
		fnRunFn: func(context.Context) error {
			return nil
		},
		fnEngineFn: func(context.Context) (engine.Interface, error) {
			return enginefake.New(), nil
		},
		fnTableFn: func(context.Context) (table.Interface, error) {
			return nil, nil
		},
		fnStateFn: func(context.Context) (state.Interface, error) {
			return statefake.New(), nil
		},
		fnTimersFn: func(context.Context) (timers.Interface, error) {
			return timersfake.New(), nil
		},
		fnRemindersFn: func(context.Context) (reminders.Interface, error) {
			return remindersfake.New(), nil
		},
		fnRuntimeStatusFn: func() *runtimev1pb.ActorRuntime {
			return nil
		},
	}
}

func (f *Fake) WithInit(fn func(actors.InitOptions) error) *Fake {
	f.fnInitFn = fn
	return f
}

func (f *Fake) WithRun(fn func(context.Context) error) *Fake {
	f.fnRunFn = fn
	return f
}

func (f *Fake) WithEngine(fn func(context.Context) (engine.Interface, error)) *Fake {
	f.fnEngineFn = fn
	return f
}

func (f *Fake) WithTable(fn func(context.Context) (table.Interface, error)) *Fake {
	f.fnTableFn = fn
	return f
}

func (f *Fake) WithState(fn func(context.Context) (state.Interface, error)) *Fake {
	f.fnStateFn = fn
	return f
}

func (f *Fake) WithTimers(fn func(context.Context) (timers.Interface, error)) *Fake {
	f.fnTimersFn = fn
	return f
}

func (f *Fake) WithReminders(fn func(context.Context) (reminders.Interface, error)) *Fake {
	f.fnRemindersFn = fn
	return f
}

func (f *Fake) WithRuntimeStatus(fn func() *runtimev1pb.ActorRuntime) *Fake {
	f.fnRuntimeStatusFn = fn
	return f
}

func (f *Fake) Init(opts actors.InitOptions) error {
	return f.fnInitFn(opts)
}

func (f *Fake) Run(ctx context.Context) error {
	return f.fnRunFn(ctx)
}

func (f *Fake) Engine(ctx context.Context) (engine.Interface, error) {
	return f.fnEngineFn(ctx)
}

func (f *Fake) Table(ctx context.Context) (table.Interface, error) {
	return f.fnTableFn(ctx)
}

func (f *Fake) State(ctx context.Context) (state.Interface, error) {
	return f.fnStateFn(ctx)
}

func (f *Fake) Timers(ctx context.Context) (timers.Interface, error) {
	return f.fnTimersFn(ctx)
}

func (f *Fake) Reminders(ctx context.Context) (reminders.Interface, error) {
	return f.fnRemindersFn(ctx)
}

func (f *Fake) RuntimeStatus() *runtimev1pb.ActorRuntime {
	return f.fnRuntimeStatusFn()
}
