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

	"github.com/dapr/dapr/pkg/actors/internal"
)

type FakeRemindersProvider struct {
	initFn                     func(ctx context.Context) error
	getReminderFn              func(ctx context.Context, req *internal.GetReminderRequest) (*internal.Reminder, error)
	createReminderFn           func(ctx context.Context, req *internal.CreateReminderRequest) error
	deleteReminderFn           func(ctx context.Context, req internal.DeleteReminderRequest) error
	listRemindersFn            func(ctx context.Context, req internal.ListRemindersRequest) ([]*internal.Reminder, error)
	drainRebalancedRemindersFn func(actorType string, actorID string)
	onPlacementTablesUpdatedFn func(ctx context.Context)
	setExecuteReminderFn       func(fn internal.ExecuteReminderFn)
	setStateStoreProviderFn    func(fn internal.StateStoreProviderFn)
	setLookupActorFn           func(fn internal.LookupActorFn)
}

func NewRemindersProvider() *FakeRemindersProvider {
	return &FakeRemindersProvider{
		initFn: func(ctx context.Context) error {
			return nil
		},
		getReminderFn: func(ctx context.Context, req *internal.GetReminderRequest) (*internal.Reminder, error) {
			return nil, nil
		},
		createReminderFn: func(ctx context.Context, req *internal.CreateReminderRequest) error {
			return nil
		},
		deleteReminderFn: func(ctx context.Context, req internal.DeleteReminderRequest) error {
			return nil
		},
		listRemindersFn: func(ctx context.Context, req internal.ListRemindersRequest) ([]*internal.Reminder, error) {
			return nil, nil
		},
		drainRebalancedRemindersFn: func(actorType string, actorID string) {},
		onPlacementTablesUpdatedFn: func(ctx context.Context) {},
		setExecuteReminderFn:       func(fn internal.ExecuteReminderFn) {},
		setStateStoreProviderFn:    func(fn internal.StateStoreProviderFn) {},
		setLookupActorFn:           func(fn internal.LookupActorFn) {},
	}
}

func (f *FakeRemindersProvider) WithInit(fn func(ctx context.Context) error) *FakeRemindersProvider {
	f.initFn = fn
	return f
}

func (f *FakeRemindersProvider) WithGetReminder(fn func(ctx context.Context, req *internal.GetReminderRequest) (*internal.Reminder, error)) *FakeRemindersProvider {
	f.getReminderFn = fn
	return f
}

func (f *FakeRemindersProvider) WithCreateReminder(fn func(ctx context.Context, req *internal.CreateReminderRequest) error) *FakeRemindersProvider {
	f.createReminderFn = fn
	return f
}

func (f *FakeRemindersProvider) WithDeleteReminder(fn func(ctx context.Context, req internal.DeleteReminderRequest) error) *FakeRemindersProvider {
	f.deleteReminderFn = fn
	return f
}

func (f *FakeRemindersProvider) WithListReminders(fn func(ctx context.Context, req internal.ListRemindersRequest) ([]*internal.Reminder, error)) *FakeRemindersProvider {
	f.listRemindersFn = fn
	return f
}

func (f *FakeRemindersProvider) WithDrainRebalancedReminders(fn func(actorType string, actorID string)) *FakeRemindersProvider {
	f.drainRebalancedRemindersFn = fn
	return f
}

func (f *FakeRemindersProvider) WithOnPlacementTablesUpdated(fn func(ctx context.Context)) *FakeRemindersProvider {
	f.onPlacementTablesUpdatedFn = fn
	return f
}

func (f *FakeRemindersProvider) WithSetExecuteReminder(fn func(fn internal.ExecuteReminderFn)) *FakeRemindersProvider {
	f.setExecuteReminderFn = fn
	return f
}

func (f *FakeRemindersProvider) WithSetStateStoreProvider(fn func(fn internal.StateStoreProviderFn)) *FakeRemindersProvider {
	f.setStateStoreProviderFn = fn
	return f
}

func (f *FakeRemindersProvider) WithSetLookupActor(fn func(fn internal.LookupActorFn)) *FakeRemindersProvider {
	f.setLookupActorFn = fn
	return f
}

func (f *FakeRemindersProvider) Init(ctx context.Context) error {
	return f.initFn(ctx)
}

func (f *FakeRemindersProvider) GetReminder(ctx context.Context, req *internal.GetReminderRequest) (*internal.Reminder, error) {
	return f.getReminderFn(ctx, req)
}

func (f *FakeRemindersProvider) CreateReminder(ctx context.Context, req *internal.CreateReminderRequest) error {
	return f.createReminderFn(ctx, req)
}

func (f *FakeRemindersProvider) DeleteReminder(ctx context.Context, req internal.DeleteReminderRequest) error {
	return f.deleteReminderFn(ctx, req)
}

func (f *FakeRemindersProvider) ListReminders(ctx context.Context, req internal.ListRemindersRequest) ([]*internal.Reminder, error) {
	return f.listRemindersFn(ctx, req)
}

func (f *FakeRemindersProvider) DrainRebalancedReminders(actorType string, actorID string) {
	f.drainRebalancedRemindersFn(actorType, actorID)
}

func (f *FakeRemindersProvider) OnPlacementTablesUpdated(ctx context.Context) {
	f.onPlacementTablesUpdatedFn(ctx)
}

func (f *FakeRemindersProvider) SetExecuteReminderFn(fn internal.ExecuteReminderFn) {
	f.setExecuteReminderFn(fn)
}

func (f *FakeRemindersProvider) SetStateStoreProviderFn(fn internal.StateStoreProviderFn) {
	f.setStateStoreProviderFn(fn)
}

func (f *FakeRemindersProvider) SetLookupActorFn(fn internal.LookupActorFn) {
	f.setLookupActorFn(fn)
}

func (f *FakeRemindersProvider) Close() error {
	return nil
}
