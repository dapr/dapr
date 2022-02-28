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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoOp(t *testing.T) {
	ctx := context.Background()
	policy := &NoOp{}

	tests := []struct {
		name string
		fn   func(ctx context.Context) Runner
	}{
		{
			name: "route",
			fn: func(ctx context.Context) Runner {
				return policy.RoutePolicy(ctx, "test")
			},
		},
		{
			name: "endpoint",
			fn: func(ctx context.Context) Runner {
				return policy.EndpointPolicy(ctx, "test", "test")
			},
		},
		{
			name: "actor",
			fn: func(ctx context.Context) Runner {
				return policy.ActorPolicy(ctx, "test", "test")
			},
		},
		{
			name: "component",
			fn: func(ctx context.Context) Runner {
				return policy.ComponentPolicy(ctx, "test")
			},
		},
	}

	for _, tt := range tests {
		runner := tt.fn(ctx)
		called := false
		err := runner(func(passedCtx context.Context) error {
			assert.Equal(t, ctx, passedCtx)
			called = true
			return nil
		})
		assert.NoError(t, err)
		assert.True(t, called)
	}
}
