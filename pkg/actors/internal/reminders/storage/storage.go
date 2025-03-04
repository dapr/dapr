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

package storage

import (
	"context"
	"io"

	"github.com/dapr/dapr/pkg/actors/api"
)

// Interface is the interface for the object that provides reminders backend
// storage.
type Interface interface {
	io.Closer

	Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error)
	Create(ctx context.Context, req *api.CreateReminderRequest) error
	Delete(ctx context.Context, req *api.DeleteReminderRequest) error
	List(ctx context.Context, req *api.ListRemindersRequest) ([]*api.Reminder, error)
	DrainRebalancedReminders()
	OnPlacementTablesUpdated(ctx context.Context, lookupFn func(context.Context, *api.LookupActorRequest) bool)
}
