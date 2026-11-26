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

package list

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/messages"
)

// keysStore is an actor state store which lists the given keys.
type keysStore struct {
	actorstate.Backend
	keys    []string
	pattern string
}

func (s *keysStore) KeysLike(_ context.Context, req *state.KeysLikeRequest) (*state.KeysLikeResponse, error) {
	s.pattern = req.Pattern
	return &state.KeysLikeResponse{Keys: s.keys}, nil
}

// resolverFunc resolves every actor type with a function.
type resolverFunc func(actorType string) (string, actorstate.Backend, error)

func (f resolverFunc) Resolve(actorType string) (string, actorstate.Backend, error) {
	return f(actorType)
}

func TestListInstanceIDs(t *testing.T) {
	const workflowType = "dapr.internal.ns.app.workflow"

	t.Run("lists from the store resolved for the workflow actor type", func(t *testing.T) {
		store := &keysStore{keys: []string{
			"app||" + workflowType + "||id1||metadata",
			"app||" + workflowType + "||id2||metadata",
		}}
		var resolved string
		resp, err := ListInstanceIDs(t.Context(), ListOptions{
			StoreResolver: resolverFunc(func(actorType string) (string, actorstate.Backend, error) {
				resolved = actorType
				return "wallet-workflows", store, nil
			}),
			WorkflowActorType: workflowType,
			AppID:             "app",
		})
		require.NoError(t, err)
		assert.Equal(t, workflowType, resolved)
		assert.Equal(t, "app||"+workflowType+"||%||metadata", store.pattern)
		assert.Equal(t, []string{"id1", "id2"}, resp.Keys)
	})

	t.Run("a store that cannot be resolved fails with its error", func(t *testing.T) {
		notFound := messages.ErrActorStateStoreNotFound.WithFormat(workflowType, "wallet-workflows")
		_, err := ListInstanceIDs(t.Context(), ListOptions{
			StoreResolver: resolverFunc(func(string) (string, actorstate.Backend, error) {
				return "", nil, notFound
			}),
			WorkflowActorType: workflowType,
			AppID:             "app",
		})
		require.ErrorIs(t, err, notFound)
	})

	t.Run("a store that cannot list keys fails", func(t *testing.T) {
		_, err := ListInstanceIDs(t.Context(), ListOptions{
			StoreResolver: resolverFunc(func(string) (string, actorstate.Backend, error) {
				return "wallet-workflows", struct{ actorstate.Backend }{}, nil
			}),
			WorkflowActorType: workflowType,
			AppID:             "app",
		})
		require.ErrorContains(t, err, "state store wallet-workflows does not support listing keys")
	})
}
