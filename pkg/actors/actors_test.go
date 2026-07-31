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

package actors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribstate "github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

func TestHostValidation(t *testing.T) {
	t.Parallel()

	t.Run("kubernetes mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "")
		require.Error(t, err)
	})

	t.Run("kubernetes mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.KubernetesMode, "")
		require.NoError(t, err)
	})

	t.Run("kubernetes mode with mTLS and namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "default")
		require.NoError(t, err)
	})

	t.Run("self hosted mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.StandaloneMode, "")
		require.NoError(t, err)
	})

	t.Run("self hosted mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.StandaloneMode, "")
		require.NoError(t, err)
	})
}

func TestConvergeHosting(t *testing.T) {
	t.Parallel()

	newConverge := func(t *testing.T) (*actors, *compstore.ComponentStore, *int, *int) {
		t.Helper()

		var suspends, resumes int
		cs := compstore.New()
		a := &actors{
			compStore: cs,
			table: tablefake.New().
				WithSuspendHosting(func(context.Context) error {
					suspends++
					return nil
				}).
				WithResumeHosting(func() {
					resumes++
				}),
		}
		_, a.hostingName, a.hostingRev, a.hostingActive = cs.GetStateStoreActorWithRevision()
		return a, cs, &suspends, &resumes
	}

	store := func(t *testing.T) contribstate.Store {
		t.Helper()
		return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
	}

	t.Run("no change is a no-op", func(t *testing.T) {
		t.Parallel()
		a, _, suspends, resumes := newConverge(t)

		a.convergeHosting(t.Context())
		assert.Equal(t, 0, *suspends)
		assert.Equal(t, 0, *resumes)
	})

	t.Run("store added resumes without draining", func(t *testing.T) {
		t.Parallel()
		a, cs, suspends, resumes := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())
		assert.Equal(t, 0, *suspends)
		assert.Equal(t, 1, *resumes)

		// Converging again with no further change is a no-op.
		a.convergeHosting(t.Context())
		assert.Equal(t, 0, *suspends)
		assert.Equal(t, 1, *resumes)
	})

	t.Run("store removed drains and stays suspended", func(t *testing.T) {
		t.Parallel()
		a, cs, suspends, resumes := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())

		cs.DeleteStateStore("mystore")
		a.convergeHosting(t.Context())
		assert.Equal(t, 1, *suspends)
		assert.Equal(t, 1, *resumes)
	})

	t.Run("same-name update swaps in place without draining", func(t *testing.T) {
		t.Parallel()
		a, cs, suspends, resumes := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())

		// A same-name update is a delete immediately followed by an add; the
		// data path resolves the store per call so hosted actors continue
		// against the new instance without a drain cycle.
		cs.DeleteStateStore("mystore")
		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())
		assert.Equal(t, 0, *suspends)
		assert.Equal(t, 1, *resumes)
	})

	t.Run("renamed store drains then resumes", func(t *testing.T) {
		t.Parallel()
		a, cs, suspends, resumes := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())

		// A different component becoming the actor state store is a
		// different backing store, so hosted actors are drained.
		cs.DeleteStateStore("mystore")
		require.NoError(t, cs.AddStateStoreActor("otherstore", store(t)))
		a.convergeHosting(t.Context())
		assert.Equal(t, 1, *suspends)
		assert.Equal(t, 2, *resumes)
	})
}

func TestOnActorStateStoreChanged(t *testing.T) {
	t.Parallel()

	a := &actors{storeKickCh: make(chan struct{}, 1)}
	// Never blocks, regardless of how many notifications are outstanding.
	a.OnActorStateStoreChanged()
	a.OnActorStateStoreChanged()
	a.OnActorStateStoreChanged()

	select {
	case <-a.storeKickCh:
	default:
		t.Fatal("expected a latched kick")
	}
	select {
	case <-a.storeKickCh:
		t.Fatal("expected kicks to coalesce")
	default:
	}
}
