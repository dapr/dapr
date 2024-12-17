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
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type Fake struct {
	callFn         func(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	callReminderFn func(context.Context, *api.Reminder) error
}

func New() *Fake {
	return &Fake{
		callFn: func(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
			return &internalv1pb.InternalInvokeResponse{
				Message: &commonv1.InvokeResponse{},
			}, nil
		},
		callReminderFn: func(context.Context, *api.Reminder) error {
			return nil
		},
	}
}

func (f *Fake) WithCallFn(fn func(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)) *Fake {
	f.callFn = fn
	return f
}

func (f *Fake) WithCallReminderFn(fn func(context.Context, *api.Reminder) error) *Fake {
	f.callReminderFn = fn
	return f
}

func (f *Fake) Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	return f.callFn(ctx, req)
}

func (f *Fake) CallReminder(ctx context.Context, reminder *api.Reminder) error {
	return f.callReminderFn(ctx, reminder)
}
