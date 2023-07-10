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

package core

import (
	"context"
	"time"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/core/reminder"
)

//nolint:interfacebloat
type Reminders interface {
	GetReminder(ctx context.Context, req *reminder.GetReminderRequest) (*Reminder, error)
	CreateReminder(ctx context.Context, req *reminder.CreateReminderRequest) error
	DeleteReminder(ctx context.Context, req *reminder.DeleteReminderRequest) error
	RenameReminder(ctx context.Context, req *reminder.RenameReminderRequest) error
	SetStateStore(store TransactionalStateStore)
	ExecuteStateStoreTransaction(ctx context.Context, store TransactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error
	StartReminder(reminder *Reminder, stopChannel chan struct{}) error
	ExecuteReminder(reminder *Reminder, isTimer bool) (err error)
	GetActorTypeMetadata(ctx context.Context, actorType string, migrate bool) (*ActorMetadata, error)
	GetReminderTrack(ctx context.Context, key string) (*reminder.ReminderTrack, error)
	UpdateReminderTrack(ctx context.Context, key string, repetition int, lastInvokeTime time.Time, etag *string) error
}
