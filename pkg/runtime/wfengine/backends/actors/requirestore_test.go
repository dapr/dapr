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

package actors

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/dapr/pkg/actors"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/kit/logger"
)

// TestRequireActorStateStore checks that the workflow operations require the
// default actor state store, which hosting the workflow actor types needs,
// and the state store bound to the workflow actor type by
// spec.workflow.stateStore.
func TestRequireActorStateStore(t *testing.T) {
	const workflowType = "dapr.internal.default.myapp.workflow"
	newStore := func() state.Store {
		return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
	}

	newBackend := func(t *testing.T, withDefault, withBound bool) *Actors {
		t.Helper()
		cs := compstore.New()
		if withDefault {
			require.NoError(t, cs.AddStateStoreActor("mystore", newStore()))
		}
		if withBound {
			cs.AddStateStore("wallet-workflows", newStore())
		}
		a := actorsWithResolver(cs, map[string]string{workflowType: "wallet-workflows"})
		be, err := New(Options{AppID: "myapp", Namespace: "default", ComponentStore: cs, Actors: a})
		require.NoError(t, err)
		return be
	}

	t.Run("no default actor state store", func(t *testing.T) {
		for _, target := range []string{"", "myapp", "otherapp"} {
			require.ErrorIs(t, newBackend(t, false, true).requireActorStateStore(target), messages.ErrActorRuntimeNotFound)
		}
	})

	t.Run("bound store not loaded", func(t *testing.T) {
		for _, target := range []string{"", "myapp"} {
			err := newBackend(t, true, false).requireActorStateStore(target)
			require.ErrorContains(t, err, "is bound to state store wallet-workflows, which is not loaded")
		}
	})

	// An operation on another app's workflow uses that app's store, not the
	// one bound to this app's workflow actor types.
	t.Run("bound store not loaded, another app's workflow", func(t *testing.T) {
		require.NoError(t, newBackend(t, true, false).requireActorStateStore("otherapp"))
	})

	t.Run("both stores loaded", func(t *testing.T) {
		require.NoError(t, newBackend(t, true, true).requireActorStateStore(""))
	})

	// Listing reads the bound store, but fails like the other operations
	// without the default actor state store.
	t.Run("listing requires the default actor state store", func(t *testing.T) {
		_, err := newBackend(t, false, true).ListInstanceIDs(t.Context(), new(protos.ListInstanceIDsRequest))
		require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)

		_, err = newBackend(t, true, false).ListInstanceIDs(t.Context(), new(protos.ListInstanceIDsRequest))
		require.ErrorContains(t, err, "is bound to state store wallet-workflows, which is not loaded")
	})
}

// actorsWithResolver returns a fake actor runtime whose state store resolver
// resolves the stores of cs with bindings.
func actorsWithResolver(cs *compstore.ComponentStore, bindings map[string]string) actors.Interface {
	resolver := actorstate.NewResolver(actorstate.ResolverOptions{Stores: cs, Bindings: bindings})
	return actorsfake.New().WithStateStoreResolver(func() actorstate.StoreResolver {
		return resolver
	})
}
