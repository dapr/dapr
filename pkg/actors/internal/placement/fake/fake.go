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
	"time"

	"github.com/dapr/dapr/pkg/actors/api"
)

type Fake struct {
	fnRun           func(context.Context) error
	fnReady         func() bool
	fnLock          func(context.Context) (context.Context, context.CancelCauseFunc, error)
	fnLookupActor   func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error)
	fnIsActorHosted func(context.Context, string, string) bool
}

func New() *Fake {
	return &Fake{
		fnRun:   func(ctx context.Context) error { <-ctx.Done(); return nil },
		fnReady: func() bool { return true },
		fnLock: func(ctx context.Context) (context.Context, context.CancelCauseFunc, error) {
			return ctx, func(_ error) {}, nil
		},
		fnLookupActor: func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
			return nil, ctx, func(_ error) {}, nil
		},
		fnIsActorHosted: func(ctx context.Context, actorType, actorID string) bool {
			return false
		},
	}
}

func (f *Fake) WithRun(fn func(context.Context) error) *Fake {
	f.fnRun = fn
	return f
}

func (f *Fake) WithReady(fn func() bool) *Fake {
	f.fnReady = fn
	return f
}

func (f *Fake) WithLock(fn func(context.Context) (context.Context, context.CancelCauseFunc, error)) *Fake {
	f.fnLock = fn
	return f
}

func (f *Fake) WithLookupActor(fn func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error)) *Fake {
	f.fnLookupActor = fn
	return f
}

func (f *Fake) WithIsActorHosted(fn func(context.Context, string, string) bool) *Fake {
	f.fnIsActorHosted = fn
	return f
}

func (f *Fake) Run(ctx context.Context) error {
	return f.fnRun(ctx)
}

func (f *Fake) Ready() bool {
	return f.fnReady()
}

func (f *Fake) Lock(ctx context.Context) (context.Context, context.CancelCauseFunc, error) {
	return f.fnLock(ctx)
}

func (f *Fake) LookupActor(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
	return f.fnLookupActor(ctx, req)
}

func (f *Fake) IsActorHosted(ctx context.Context, actorType, actorID string) bool {
	return f.fnIsActorHosted(ctx, actorType, actorID)
}

func (f *Fake) SetDrainOngoingCallTimeout(*bool, *time.Duration) {
}
