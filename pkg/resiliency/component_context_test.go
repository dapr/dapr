/*
Copyright 2026 The Dapr Authors
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
	"github.com/stretchr/testify/require"
)

type svidCtxKey struct{}

// markSVID mimics security.Handler.WithSVIDContext: it attaches a value to the
// context that components would read (e.g. the X.509/JWT SVID source).
func markSVID(ctx context.Context) context.Context {
	return context.WithValue(ctx, svidCtxKey{}, "svid-source")
}

func hasSVID(ctx context.Context) bool {
	return ctx.Value(svidCtxKey{}) != nil
}

func TestComponentContextDecorator_AppliedForComponentPolicies(t *testing.T) {
	r := New(testLog)
	r.SetComponentContextDecorator(markSVID)

	tests := map[string]*PolicyDefinition{
		"outbound": r.ComponentOutboundPolicy("mystore", Statestore),
		"inbound":  r.ComponentInboundPolicy("mypubsub", Pubsub),
	}

	for name, def := range tests {
		t.Run(name, func(t *testing.T) {
			// The decorator must be applied to the context the operation sees.
			runner := NewRunner[bool](context.Background(), def)
			seen, err := runner(func(ctx context.Context) (bool, error) {
				return hasSVID(ctx), nil
			})
			require.NoError(t, err)
			assert.True(t, seen, "component operation context should carry the SVID source")

			// ComponentContext is the same decoration used directly at non-runner
			// seams (Subscribe/Read).
			assert.True(t, hasSVID(def.ComponentContext(context.Background())))
		})
	}
}

func TestComponentContextDecorator_NotAppliedForNonComponentPolicies(t *testing.T) {
	r := New(testLog)
	r.addBuiltInPolicies()
	r.SetComponentContextDecorator(markSVID)

	// Non-component policies must never receive the component context decoration.
	nonComponent := map[string]*PolicyDefinition{
		"endpoint":     r.EndpointPolicy("myapp", "myapp"),
		"actorPreLock": r.ActorPreLockPolicy("myactor", "id"),
		"builtIn":      r.BuiltInPolicy(BuiltInServiceRetries),
	}

	for name, def := range nonComponent {
		t.Run(name, func(t *testing.T) {
			runner := NewRunner[bool](context.Background(), def)
			seen, err := runner(func(ctx context.Context) (bool, error) {
				return hasSVID(ctx), nil
			})
			require.NoError(t, err)
			assert.False(t, seen, "non-component operation context must not carry the SVID source")
			assert.False(t, hasSVID(def.ComponentContext(context.Background())))
		})
	}
}

func TestComponentContextDecorator_NoOpWhenUnset(t *testing.T) {
	// A provider without a decorator (the default) leaves component contexts
	// untouched, preserving behaviour when mTLS/SPIFFE is disabled.
	r := New(testLog)
	def := r.ComponentOutboundPolicy("mystore", Statestore)

	runner := NewRunner[bool](context.Background(), def)
	seen, err := runner(func(ctx context.Context) (bool, error) {
		return hasSVID(ctx), nil
	})
	require.NoError(t, err)
	assert.False(t, seen)

	// A nil decorator fn is safe: ComponentContext returns the context unchanged.
	assert.NotNil(t, def.ComponentContext(context.Background()))
}
