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
)

// ErrReminderCanceled is returned when the reminder has been canceled.
var ErrReminderCanceled = errors.New("reminder has been canceled")

// ExecuteReminderFn is the type of the function invoked when a reminder is to be executed.
// If this method returns false, the reminder is canceled by the actor.
type ExecuteReminderFn func(reminder *Reminder) bool

// LookupActorFn is the type of a function that returns whether an actor is locally-hosted and the address of its host.
type LookupActorFn func(ctx context.Context, actorType string, actorID string) (isLocal bool, actorAddress string)

// StateStoreProviderFn is the type of a function that returns the state store provider and its name.
type StateStoreProviderFn func() (string, TransactionalStateStore, error)

// RemindersProvider is the interface for the object that provides reminders services.
type RemindersProvider interface {
	io.Closer

	Init(ctx context.Context) error
	GetReminder(ctx context.Context, req *GetReminderRequest) (*Reminder, error)
	CreateReminder(ctx context.Context, req *Reminder) error
	DeleteReminder(ctx context.Context, req DeleteReminderRequest) error
	DrainRebalancedReminders(actorType string, actorID string)
	OnPlacementTablesUpdated(ctx context.Context)

	SetExecuteReminderFn(fn ExecuteReminderFn)
	SetStateStoreProviderFn(fn StateStoreProviderFn)
	SetLookupActorFn(fn LookupActorFn)
}
