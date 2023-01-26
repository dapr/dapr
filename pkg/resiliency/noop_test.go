/*
Copyright 2021 The Dapr Authors
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

package resiliency

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoOp(t *testing.T) {
	ctx := context.Background()
	policy := NoOp{}

	tests := []struct {
		name string
		fn   func(ctx context.Context) Runner[any]
		err  error
	}{
		{
			name: "route",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.RoutePolicy("test"))
			},
		},
		{
			name: "route error",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.RoutePolicy("test"))
			},
			err: errors.New("route error"),
		},
		{
			name: "endpoint",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.EndpointPolicy("test", "test"))
			},
		},
		{
			name: "endpoint error",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.EndpointPolicy("test", "test"))
			},
			err: errors.New("endpoint error"),
		},
		{
			name: "actor",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.ActorPreLockPolicy("test", "test"))
			},
		},
		{
			name: "actor error",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.ActorPreLockPolicy("test", "test"))
			},
			err: errors.New("actor error"),
		},
		{
			name: "component output",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.ComponentOutboundPolicy("test", "Statestore"))
			},
		},
		{
			name: "component outbound error",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.ComponentOutboundPolicy("test", "Statestore"))
			},
			err: errors.New("component outbound error"),
		},
		{
			name: "component inbound",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.ComponentInboundPolicy("test", "Statestore"))
			},
		},
		{
			name: "component inbound error",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.ComponentInboundPolicy("test", "Statestore"))
			},
			err: errors.New("component inbound error"),
		},
		{
			name: "built-in",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.BuiltInPolicy(BuiltInServiceRetries))
			},
		},
		{
			name: "built-in error",
			fn: func(ctx context.Context) Runner[any] {
				return NewRunner[any](ctx, policy.BuiltInPolicy(BuiltInServiceRetries))
			},
			err: errors.New("built-in error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := tt.fn(ctx)
			called := atomic.Bool{}
			_, err := runner(func(passedCtx context.Context) (any, error) {
				assert.Equal(t, ctx, passedCtx)
				called.Store(true)
				return nil, tt.err
			})
			assert.Equal(t, tt.err, err)
			assert.True(t, called.Load())
		})
	}
}
