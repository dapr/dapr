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
	createFn func(context.Context, *api.CreateTimerRequest) error
	deleteFn func(context.Context, *api.DeleteTimerRequest) error
	listFn   func(context.Context, *api.ListTimersRequest) ([]*api.Reminder, error)
	getFn    func(context.Context, *api.GetTimerRequest) (*api.Reminder, error)
}

func New() *Fake {
	return &Fake{
		createFn: func(context.Context, *api.CreateTimerRequest) error { return nil },
		deleteFn: func(context.Context, *api.DeleteTimerRequest) error { return nil },
		listFn:   func(context.Context, *api.ListTimersRequest) ([]*api.Reminder, error) { return nil, nil },
		getFn:    func(context.Context, *api.GetTimerRequest) (*api.Reminder, error) { return nil, nil },
	}
}

func (f *Fake) WithCreateFn(fn func(context.Context, *api.CreateTimerRequest) error) *Fake {
	f.createFn = fn
	return f
}

func (f *Fake) WithDeleteFn(fn func(context.Context, *api.DeleteTimerRequest) error) *Fake {
	f.deleteFn = fn
	return f
}

func (f *Fake) WithListFn(fn func(context.Context, *api.ListTimersRequest) ([]*api.Reminder, error)) *Fake {
	f.listFn = fn
	return f
}

func (f *Fake) WithGetFn(fn func(context.Context, *api.GetTimerRequest) (*api.Reminder, error)) *Fake {
	f.getFn = fn
	return f
}

func (f *Fake) Create(ctx context.Context, req *api.CreateTimerRequest) error {
	return f.createFn(ctx, req)
}

func (f *Fake) Delete(ctx context.Context, req *api.DeleteTimerRequest) error {
	return f.deleteFn(ctx, req)
}

func (f *Fake) List(ctx context.Context, req *api.ListTimersRequest) ([]*api.Reminder, error) {
	return f.listFn(ctx, req)
}

func (f *Fake) Get(ctx context.Context, req *api.GetTimerRequest) (*api.Reminder, error) {
	return f.getFn(ctx, req)
}
