/*
Copyright 2026 The Dapr Authors
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

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
)

type Fake struct {
	closeFn                  func() error
	typesFn                  func() []string
	reminderTypesFn          func() []string
	setReminderDeliveryFn    func(bool, ...string)
	subscribeReminderTypesFn func(context.Context) <-chan struct{}
	drainActorTypesFn        func(...string)
	completeDrainsFn         func(context.Context, func(string) bool)
	isActorTypeHostedFn      func(string) bool
	getOrCreateFn            func(context.Context, string, string) (targets.Interface, error)
	actorExistsFn            func(string, string) bool
	registerActorTypesFn     func(table.RegisterActorTypeOptions)
	unRegisterActorTypesFn   func(...string) error
	subscribeToTypeUpdatesFn func(context.Context) (<-chan []string, []string)
	haltAllFn                func(context.Context) error
	haltNonHostedFn          func(context.Context, func(*api.LookupActorRequest) bool) error
	lenFn                    func() map[string]int
	suspendHostingFn         func(context.Context) error
	resumeHostingFn          func()
}

func New() *Fake {
	return &Fake{
		closeFn:                  func() error { return nil },
		typesFn:                  func() []string { return nil },
		reminderTypesFn:          func() []string { return nil },
		setReminderDeliveryFn:    func(bool, ...string) {},
		subscribeReminderTypesFn: func(context.Context) <-chan struct{} { return nil },
		drainActorTypesFn:        func(...string) {},
		completeDrainsFn:         func(context.Context, func(string) bool) {},
		isActorTypeHostedFn:      func(string) bool { return false },
		getOrCreateFn:            func(context.Context, string, string) (targets.Interface, error) { return nil, nil },
		actorExistsFn:            func(string, string) bool { return false },
		registerActorTypesFn:     func(table.RegisterActorTypeOptions) {},
		unRegisterActorTypesFn:   func(...string) error { return nil },
		subscribeToTypeUpdatesFn: func(context.Context) (<-chan []string, []string) { return nil, nil },
		haltAllFn:                func(context.Context) error { return nil },
		haltNonHostedFn:          func(context.Context, func(*api.LookupActorRequest) bool) error { return nil },
		lenFn:                    func() map[string]int { return nil },
		suspendHostingFn:         func(context.Context) error { return nil },
		resumeHostingFn:          func() {},
	}
}

func (f *Fake) WithClose(fn func() error) *Fake    { f.closeFn = fn; return f }
func (f *Fake) WithTypes(fn func() []string) *Fake { f.typesFn = fn; return f }

func (f *Fake) WithReminderTypes(fn func() []string) *Fake { f.reminderTypesFn = fn; return f }

func (f *Fake) WithSetReminderDelivery(fn func(bool, ...string)) *Fake {
	f.setReminderDeliveryFn = fn
	return f
}

func (f *Fake) WithDrainActorTypes(fn func(...string)) *Fake {
	f.drainActorTypesFn = fn
	return f
}

func (f *Fake) WithCompleteDrains(fn func(context.Context, func(string) bool)) *Fake {
	f.completeDrainsFn = fn
	return f
}

func (f *Fake) WithSubscribeToReminderTypeUpdates(fn func(context.Context) <-chan struct{}) *Fake {
	f.subscribeReminderTypesFn = fn
	return f
}

func (f *Fake) WithIsActorTypeHosted(fn func(string) bool) *Fake {
	f.isActorTypeHostedFn = fn
	return f
}

func (f *Fake) WithGetOrCreate(fn func(context.Context, string, string) (targets.Interface, error)) *Fake {
	f.getOrCreateFn = fn
	return f
}
func (f *Fake) WithActorExists(fn func(string, string) bool) *Fake { f.actorExistsFn = fn; return f }
func (f *Fake) WithRegisterActorTypes(fn func(table.RegisterActorTypeOptions)) *Fake {
	f.registerActorTypesFn = fn
	return f
}

func (f *Fake) WithUnRegisterActorTypes(fn func(...string) error) *Fake {
	f.unRegisterActorTypesFn = fn
	return f
}

func (f *Fake) WithSubscribeToTypeUpdates(fn func(context.Context) (<-chan []string, []string)) *Fake {
	f.subscribeToTypeUpdatesFn = fn
	return f
}
func (f *Fake) WithHaltAll(fn func(context.Context) error) *Fake { f.haltAllFn = fn; return f }
func (f *Fake) WithHaltNonHosted(fn func(context.Context, func(*api.LookupActorRequest) bool) error) *Fake {
	f.haltNonHostedFn = fn
	return f
}
func (f *Fake) WithLen(fn func() map[string]int) *Fake { f.lenFn = fn; return f }

func (f *Fake) WithSuspendHosting(fn func(context.Context) error) *Fake {
	f.suspendHostingFn = fn
	return f
}

func (f *Fake) WithResumeHosting(fn func()) *Fake {
	f.resumeHostingFn = fn
	return f
}

func (f *Fake) Close() error                            { return f.closeFn() }
func (f *Fake) Types() []string                         { return f.typesFn() }
func (f *Fake) ReminderTypes() []string                 { return f.reminderTypesFn() }
func (f *Fake) IsActorTypeHosted(actorType string) bool { return f.isActorTypeHostedFn(actorType) }
func (f *Fake) GetOrCreate(ctx context.Context, actorType, actorID string) (targets.Interface, error) {
	return f.getOrCreateFn(ctx, actorType, actorID)
}

func (f *Fake) ActorExists(actorType, actorKey string) bool {
	return f.actorExistsFn(actorType, actorKey)
}
func (f *Fake) RegisterActorTypes(opts table.RegisterActorTypeOptions) { f.registerActorTypesFn(opts) }
func (f *Fake) UnRegisterActorTypes(actorTypes ...string) error {
	return f.unRegisterActorTypesFn(actorTypes...)
}

func (f *Fake) SubscribeToTypeUpdates(ctx context.Context) (<-chan []string, []string) {
	return f.subscribeToTypeUpdatesFn(ctx)
}
func (f *Fake) HaltAll(ctx context.Context) error { return f.haltAllFn(ctx) }
func (f *Fake) HaltNonHosted(ctx context.Context, fn func(*api.LookupActorRequest) bool) error {
	return f.haltNonHostedFn(ctx, fn)
}
func (f *Fake) Len() map[string]int { return f.lenFn() }

func (f *Fake) SetReminderDelivery(enabled bool, actorTypes ...string) {
	f.setReminderDeliveryFn(enabled, actorTypes...)
}

func (f *Fake) SubscribeToReminderTypeUpdates(ctx context.Context) <-chan struct{} {
	return f.subscribeReminderTypesFn(ctx)
}

func (f *Fake) DrainActorTypes(actorTypes ...string) { f.drainActorTypesFn(actorTypes...) }
func (f *Fake) CompleteDrains(ctx context.Context, placed func(string) bool) {
	f.completeDrainsFn(ctx, placed)
}

func (f *Fake) SuspendHosting(ctx context.Context) error { return f.suspendHostingFn(ctx) }
func (f *Fake) ResumeHosting()                           { f.resumeHostingFn() }
