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
	"github.com/dapr/dapr/pkg/actors/hostconfig"
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
	fnInit                   func(actors.InitOptions) error
	fnRun                    func(context.Context) error
	fnEngine                 func(context.Context) (engine.Interface, error)
	fnTable                  func(context.Context) (table.Interface, error)
	fnState                  func(context.Context) (state.Interface, error)
	fnTimers                 func(context.Context) (timers.Interface, error)
	fnReminders              func(context.Context) (reminders.Interface, error)
	fnRuntimeStatus          func() *runtimev1pb.ActorRuntime
	fnRegisterHosted         func(hostconfig.Config) error
	fnUnRegisterHosted       func(actorTypes ...string)
	fnWaitForRegisteredHosts func(ctx context.Context) error
}

func New() *Fake {
	return &Fake{
		fnInit: func(actors.InitOptions) error {
			return nil
		},
		fnRun: func(context.Context) error {
			return nil
		},
		fnEngine: func(context.Context) (engine.Interface, error) {
			return enginefake.New(), nil
		},
		fnTable: func(context.Context) (table.Interface, error) {
			return nil, nil
		},
		fnState: func(context.Context) (state.Interface, error) {
			return statefake.New(), nil
		},
		fnTimers: func(context.Context) (timers.Interface, error) {
			return timersfake.New(), nil
		},
		fnReminders: func(context.Context) (reminders.Interface, error) {
			return remindersfake.New(), nil
		},
		fnRuntimeStatus: func() *runtimev1pb.ActorRuntime {
			return nil
		},
		fnRegisterHosted: func(hostconfig.Config) error {
			return nil
		},
		fnUnRegisterHosted: func(...string) {},
		fnWaitForRegisteredHosts: func(context.Context) error {
			return nil
		},
	}
}

func (f *Fake) WithInit(fn func(actors.InitOptions) error) *Fake {
	f.fnInit = fn
	return f
}

func (f *Fake) WithRun(fn func(context.Context) error) *Fake {
	f.fnRun = fn
	return f
}

func (f *Fake) WithEngine(fn func(context.Context) (engine.Interface, error)) *Fake {
	f.fnEngine = fn
	return f
}

func (f *Fake) WithTable(fn func(context.Context) (table.Interface, error)) *Fake {
	f.fnTable = fn
	return f
}

func (f *Fake) WithState(fn func(context.Context) (state.Interface, error)) *Fake {
	f.fnState = fn
	return f
}

func (f *Fake) WithTimers(fn func(context.Context) (timers.Interface, error)) *Fake {
	f.fnTimers = fn
	return f
}

func (f *Fake) WithReminders(fn func(context.Context) (reminders.Interface, error)) *Fake {
	f.fnReminders = fn
	return f
}

func (f *Fake) WithRuntimeStatus(fn func() *runtimev1pb.ActorRuntime) *Fake {
	f.fnRuntimeStatus = fn
	return f
}

func (f *Fake) WithRegisterHosted(fn func(hostconfig.Config) error) *Fake {
	f.fnRegisterHosted = fn
	return f
}

func (f *Fake) WithUnRegisterHosted(fn func(...string)) *Fake {
	f.fnUnRegisterHosted = fn
	return f
}

func (f *Fake) WithWaitForRegisteredHosts(fn func(context.Context) error) *Fake {
	f.fnWaitForRegisteredHosts = fn
	return f
}

func (f *Fake) Init(opts actors.InitOptions) error {
	return f.fnInit(opts)
}

func (f *Fake) Run(ctx context.Context) error {
	return f.fnRun(ctx)
}

func (f *Fake) Engine(ctx context.Context) (engine.Interface, error) {
	return f.fnEngine(ctx)
}

func (f *Fake) Table(ctx context.Context) (table.Interface, error) {
	return f.fnTable(ctx)
}

func (f *Fake) State(ctx context.Context) (state.Interface, error) {
	return f.fnState(ctx)
}

func (f *Fake) Timers(ctx context.Context) (timers.Interface, error) {
	return f.fnTimers(ctx)
}

func (f *Fake) Reminders(ctx context.Context) (reminders.Interface, error) {
	return f.fnReminders(ctx)
}

func (f *Fake) RuntimeStatus() *runtimev1pb.ActorRuntime {
	return f.fnRuntimeStatus()
}

func (f *Fake) RegisterHosted(cfg hostconfig.Config) error {
	return f.fnRegisterHosted(cfg)
}

func (f *Fake) WaitForRegisteredHosts(ctx context.Context) error {
	return f.fnWaitForRegisteredHosts(ctx)
}

func (f *Fake) UnRegisterHosted(ids ...string) {
	f.fnUnRegisterHosted(ids...)
}
