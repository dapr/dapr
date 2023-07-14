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

package internal

import (
	"context"
	"errors"
	"io"

	"github.com/dapr/dapr/pkg/resiliency"
)

// ErrReminderCanceled is returned when the reminder has been canceled.
var ErrReminderCanceled = errors.New("reminder has been canceled")

// ExecuteReminderFn is the type of the function invoked when a re,omder is to be executed.
// If this method returns false, the reminder is canceled by the actor.
type ExecuteReminderFn func(reminder *Reminder) bool

// RemindersProvider is the interface for the object that provides reminders services.
type RemindersProvider interface {
	io.Closer

	Init(ctx context.Context) // <- this performs the tasks of "evaluateReminders" too, which are done during init
	GetReminder(ctx context.Context, req *GetReminderRequest) (*Reminder, error)
	CreateReminder(ctx context.Context, req *Reminder) error
	DeleteReminder(ctx context.Context, req DeleteReminderRequest) error
	RenameReminder(ctx context.Context, req *RenameReminderRequest) error
	DrainRebalancedReminders(actorType string, actorID string, actorKey string)

	SetExecuteReminderFn(fn ExecuteReminderFn)
	SetStateStoreProvider(fn func() (TransactionalStateStore, error))
	SetResiliencyProvider(resiliency resiliency.Provider)
	SetLookupActorFn(fn func(string, string) (bool, string))
}
