/*
Copyright 2025 The Dapr Authors
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
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/targets"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type Fake struct {
	fnKey  func() string
	fnType func() string
	fnID   func() string

	fnInvokeMethod   func(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	fnInvokeReminder func(context.Context, *api.Reminder) error
	fnInvokeTimer    func(context.Context, *api.Reminder) error
	fnInvokeStream   func(context.Context, *internalv1pb.InternalInvokeRequest, chan<- *internalv1pb.InternalInvokeResponse) error
	fnDeactivate     func() error
}

type Hook func(*Fake)

func New(actorType string, hooks ...func(*Fake)) targets.Factory {
	return func(actorID string) targets.Interface {
		f := &Fake{
			fnKey: func() string {
				return key.ConstructComposite(actorType, actorID)
			},
			fnType: func() string {
				return actorType
			},
			fnID: func() string {
				return actorID
			},
			fnInvokeMethod: func(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
				return nil, nil
			},
			fnInvokeReminder: func(ctx context.Context, reminder *api.Reminder) error {
				return nil
			},
			fnInvokeTimer: func(ctx context.Context, reminder *api.Reminder) error {
				return nil
			},
			fnInvokeStream: func(ctx context.Context, req *internalv1pb.InternalInvokeRequest, stream chan<- *internalv1pb.InternalInvokeResponse) error {
				return nil
			},
			fnDeactivate: func() error {
				return nil
			},
		}

		for _, hook := range hooks {
			hook(f)
		}

		return f
	}
}

func (f *Fake) WithKey(fn func() string) *Fake {
	f.fnKey = fn
	return f
}

func (f *Fake) WithInvokeMethod(fn func(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)) *Fake {
	f.fnInvokeMethod = fn
	return f
}

func (f *Fake) WithInvokeReminder(fn func(context.Context, *api.Reminder) error) *Fake {
	f.fnInvokeReminder = fn
	return f
}

func (f *Fake) WithInvokeTimer(fn func(context.Context, *api.Reminder) error) *Fake {
	f.fnInvokeTimer = fn
	return f
}

func (f *Fake) WithInvokeStream(fn func(context.Context, *internalv1pb.InternalInvokeRequest, chan<- *internalv1pb.InternalInvokeResponse) error) *Fake {
	f.fnInvokeStream = fn
	return f
}

func (f *Fake) WithDeactivate(fn func() error) *Fake {
	f.fnDeactivate = fn
	return f
}

func (f *Fake) Key() string {
	return f.fnKey()
}

func (f *Fake) Type() string {
	return f.fnType()
}

func (f *Fake) ID() string {
	return f.fnID()
}

func (f *Fake) InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	return f.fnInvokeMethod(ctx, req)
}

func (f *Fake) InvokeReminder(ctx context.Context, reminder *api.Reminder) error {
	return f.fnInvokeReminder(ctx, reminder)
}

func (f *Fake) InvokeTimer(ctx context.Context, reminder *api.Reminder) error {
	return f.fnInvokeTimer(ctx, reminder)
}

func (f *Fake) InvokeStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, stream chan<- *internalv1pb.InternalInvokeResponse) error {
	return f.fnInvokeStream(ctx, req, stream)
}

func (f *Fake) Deactivate() error {
	return f.fnDeactivate()
}
