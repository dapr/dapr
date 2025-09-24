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
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage"
	"github.com/dapr/dapr/pkg/actors/table"
)

// TODO: @joshvanl: move errors package
var (
	ErrReminderOpActorNotHosted = errors.New("operations on actor reminders are only possible on hosted actor types")
	ErrReminderStorageNotSet    = errors.New("reminder storage is not configured")
)

type Interface interface {
	// Get retrieves an actor reminder.
	Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error)

	// Create creates an actor reminder.
	Create(ctx context.Context, req *api.CreateReminderRequest) error

	// Delete deletes an actor reminder.
	Delete(ctx context.Context, req *api.DeleteReminderRequest) error
}

type Options struct {
	Storage storage.Interface
	Table   table.Interface
}

type reminders struct {
	storage storage.Interface
	table   table.Interface
}

func New(opts Options) Interface {
	return &reminders{
		storage: opts.Storage,
		table:   opts.Table,
	}
}

func (r *reminders) Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error) {
	if r.storage == nil {
		return nil, ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return nil, ErrReminderOpActorNotHosted
	}

	return r.storage.Get(ctx, req)
}

func (r *reminders) Create(ctx context.Context, req *api.CreateReminderRequest) error {
	if r.storage == nil {
		return ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	return r.storage.Create(ctx, req)
}

func (r *reminders) Delete(ctx context.Context, req *api.DeleteReminderRequest) error {
	if r.storage == nil {
		return ErrReminderStorageNotSet
	}

	if !r.table.IsActorTypeHosted(req.ActorType) {
		return ErrReminderOpActorNotHosted
	}

	return r.storage.Delete(ctx, req)
}
