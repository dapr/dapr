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
	"errors"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/scheduler"
	"github.com/dapr/dapr/pkg/actors/table"
)

// TODO: @joshvanl: move errors package
var (
	ErrReminderOpActorNotHosted = errors.New("operations on actor reminders are only possible on hosted actor types")
	ErrReminderStorageNotSet    = errors.New("reminder scheduler is not configured")
)

type Interface interface {
	// Get retrieves an actor reminder.
	Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error)

	// Create creates an actor reminder.
	Create(ctx context.Context, req *api.CreateReminderRequest) error

	// Delete deletes an actor reminder.
	Delete(ctx context.Context, req *api.DeleteReminderRequest) error

	// DeleteByActorID deletes all reminders for a given actor ID.
	DeleteByActorID(ctx context.Context, req *api.DeleteRemindersByActorIDRequest) error

	// List lists all reminders for a given actor type and actor ID.
	List(ctx context.Context, req *api.ListRemindersRequest) ([]*api.Reminder, error)

	// Scheduler returns the underlying reminder scheduler.
	// Used to bypass the actor hosted check.
	Scheduler() (scheduler.Interface, error)
}

type Options struct {
	Scheduler scheduler.Interface
	Table     table.Interface
}

type reminders struct {
	scheduler scheduler.Interface
	table     table.Interface
}

func New(opts Options) Interface {
	return &reminders{
		scheduler: opts.Scheduler,
		table:     opts.Table,
	}
}

func (r *reminders) Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error) {
	if r.scheduler == nil {
		return nil, ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return nil, ErrReminderOpActorNotHosted
	}

	return r.scheduler.Get(ctx, req)
}

func (r *reminders) Create(ctx context.Context, req *api.CreateReminderRequest) error {
	if r.scheduler == nil {
		return ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	return r.scheduler.Create(ctx, req)
}

func (r *reminders) Delete(ctx context.Context, req *api.DeleteReminderRequest) error {
	if r.scheduler == nil {
		return ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	return r.scheduler.Delete(ctx, req)
}

func (r *reminders) DeleteByActorID(ctx context.Context, req *api.DeleteRemindersByActorIDRequest) error {
	if r.scheduler == nil {
		return ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	return r.scheduler.DeleteByActorID(ctx, req)
}

func (r *reminders) List(ctx context.Context, req *api.ListRemindersRequest) ([]*api.Reminder, error) {
	if r.scheduler == nil {
		return nil, ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return nil, ErrReminderOpActorNotHosted
	}

	return r.scheduler.List(ctx, req)
}

func (r *reminders) Scheduler() (scheduler.Interface, error) {
	if r.scheduler == nil {
		return nil, ErrReminderStorageNotSet
	}

	return r.scheduler, nil
}
