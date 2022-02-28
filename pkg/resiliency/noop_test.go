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
