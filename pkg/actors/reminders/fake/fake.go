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

	"github.com/dapr/dapr/pkg/actors/api"
)

type Fake struct {
	getFn    func(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error)
	createFn func(ctx context.Context, req *api.CreateReminderRequest) error
	deleteFn func(ctx context.Context, req *api.DeleteReminderRequest) error
}

func New() *Fake {
	return &Fake{
		getFn: func(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error) {
			return nil, nil
		},
		createFn: func(ctx context.Context, req *api.CreateReminderRequest) error {
			return nil
		},
		deleteFn: func(ctx context.Context, req *api.DeleteReminderRequest) error {
			return nil
		},
	}
}

func (f *Fake) WithGet(fn func(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error)) *Fake {
	f.getFn = fn
	return f
}

func (f *Fake) WithCreate(fn func(ctx context.Context, req *api.CreateReminderRequest) error) *Fake {
	f.createFn = fn
	return f
}

func (f *Fake) WithDelete(fn func(ctx context.Context, req *api.DeleteReminderRequest) error) *Fake {
	f.deleteFn = fn
	return f
}

func (f *Fake) Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error) {
	return f.getFn(ctx, req)
}

func (f *Fake) Create(ctx context.Context, req *api.CreateReminderRequest) error {
	return f.createFn(ctx, req)
}

func (f *Fake) Delete(ctx context.Context, req *api.DeleteReminderRequest) error {
	return f.deleteFn(ctx, req)
}
