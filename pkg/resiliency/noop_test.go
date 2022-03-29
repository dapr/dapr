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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoOp(t *testing.T) {
	ctx := context.Background()
	policy := &NoOp{}

	tests := []struct {
		name string
		fn   func(ctx context.Context) Runner
		err  error
	}{
		{
			name: "route",
			fn: func(ctx context.Context) Runner {
				return policy.RoutePolicy(ctx, "test")
			},
		},
		{
			name: "route error",
			fn: func(ctx context.Context) Runner {
				return policy.RoutePolicy(ctx, "test")
			},
			err: errors.New("route error"),
		},
		{
			name: "endpoint",
			fn: func(ctx context.Context) Runner {
				return policy.EndpointPolicy(ctx, "test", "test")
			},
		},
		{
			name: "endpoint error",
			fn: func(ctx context.Context) Runner {
				return policy.EndpointPolicy(ctx, "test", "test")
			},
			err: errors.New("endpoint error"),
		},
		{
			name: "actor",
			fn: func(ctx context.Context) Runner {
				return policy.ActorPreLockPolicy(ctx, "test", "test")
			},
		},
		{
			name: "actor error",
			fn: func(ctx context.Context) Runner {
				return policy.ActorPreLockPolicy(ctx, "test", "test")
			},
			err: errors.New("actor error"),
		},
		{
			name: "component output",
			fn: func(ctx context.Context) Runner {
				return policy.ComponentOutboundPolicy(ctx, "test")
			},
		},
		{
			name: "component outbound error",
			fn: func(ctx context.Context) Runner {
				return policy.ComponentOutboundPolicy(ctx, "test")
			},
			err: errors.New("component outbound error"),
		},
		{
			name: "component inbound",
			fn: func(ctx context.Context) Runner {
				return policy.ComponentInboundPolicy(ctx, "test")
			},
		},
		{
			name: "component inbound error",
			fn: func(ctx context.Context) Runner {
				return policy.ComponentInboundPolicy(ctx, "test")
			},
			err: errors.New("component inbound error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := tt.fn(ctx)
			called := false
			err := runner(func(passedCtx context.Context) error {
				assert.Equal(t, ctx, passedCtx)
				called = true
				return tt.err
			})
			assert.Equal(t, tt.err, err)
			assert.True(t, called)
		})
	}
}
