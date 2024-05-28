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

package reminders

import (
	"context"

	"github.com/dapr/dapr/pkg/actors/internal"
)

// Implements a reminders provider that does nothing when using Scheduler Service.
type remindersNoOp struct{}

func NoOpReminders() internal.RemindersProvider {
	return &remindersNoOp{}
}

func (r *remindersNoOp) SetExecuteReminderFn(fn internal.ExecuteReminderFn) {
}

func (r *remindersNoOp) SetStateStoreProviderFn(fn internal.StateStoreProviderFn) {
}

func (r *remindersNoOp) SetLookupActorFn(fn internal.LookupActorFn) {
}

func (r *remindersNoOp) SetMetricsCollectorFn(fn remindersMetricsCollectorFn) {
}

// OnPlacementTablesUpdated is invoked when the actors runtime received an updated placement tables.
func (r *remindersNoOp) OnPlacementTablesUpdated(ctx context.Context) {
}

func (r *remindersNoOp) DrainRebalancedReminders(actorType string, actorID string) {
}

func (r *remindersNoOp) CreateReminder(ctx context.Context, reminder *internal.Reminder) error {
	return nil
}

func (r *remindersNoOp) Close() error {
	return nil
}

func (r *remindersNoOp) Init(ctx context.Context) error {
	return nil
}

func (r *remindersNoOp) GetReminder(ctx context.Context, req *internal.GetReminderRequest) (*internal.Reminder, error) {
	return nil, nil
}

func (r *remindersNoOp) DeleteReminder(ctx context.Context, req internal.DeleteReminderRequest) error {
	return nil
}
