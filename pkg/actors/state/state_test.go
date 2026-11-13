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

package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/resiliency"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

// stubResolver records the actor types it is asked to resolve, and returns
// the configured store or error.
type stubResolver struct {
	seen  []string
	name  string
	store *daprt.FakeStateStore
	err   error
}

func (r *stubResolver) Resolve(actorType string) (string, Backend, error) {
	r.seen = append(r.seen, actorType)
	if r.err != nil {
		return "", nil, r.err
	}
	return r.name, r.store, nil
}

func newTestState(t *testing.T, resolver StoreResolver) Interface {
	t.Helper()
	return New(Options{
		AppID:         "app",
		StoreResolver: resolver,
		Resiliency:    resiliency.New(logger.NewLogger(t.Name())),
	})
}

func TestState_Get_ResolvesByActorType(t *testing.T) {
	t.Run("reads from the resolved store", func(t *testing.T) {
		resolver := &stubResolver{name: "wallet-transactions", store: daprt.NewFakeStateStore()}
		_, err := newTestState(t, resolver).Get(t.Context(), &api.GetStateRequest{
			ActorType: "TransactionActor", ActorID: "1", Key: "k",
		}, false)
		require.NoError(t, err)
		assert.Equal(t, []string{"TransactionActor"}, resolver.seen)
		assert.Equal(t, uint64(1), resolver.store.CallCount("Get"))
	})

	t.Run("returns the resolver error unchanged", func(t *testing.T) {
		resolveErr := messages.ErrActorStateStoreNotFound.WithFormat("TransactionActor", "wallet-transactions")
		_, err := newTestState(t, &stubResolver{err: resolveErr}).Get(t.Context(), &api.GetStateRequest{
			ActorType: "TransactionActor", ActorID: "1", Key: "k",
		}, false)
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)
	})
}

func TestState_GetBulk_ResolvesByActorType(t *testing.T) {
	t.Run("reads from the resolved store", func(t *testing.T) {
		resolver := &stubResolver{name: "wallet-transactions", store: daprt.NewFakeStateStore()}
		_, err := newTestState(t, resolver).GetBulk(t.Context(), &api.GetBulkStateRequest{
			ActorType: "TransactionActor", ActorID: "1", Keys: []string{"a", "b"},
		}, false)
		require.NoError(t, err)
		assert.Equal(t, []string{"TransactionActor"}, resolver.seen)
		assert.Equal(t, uint64(1), resolver.store.CallCount("BulkGet"))
	})

	t.Run("returns the resolver error unchanged", func(t *testing.T) {
		resolveErr := messages.ErrActorStateStoreNotFound.WithFormat("TransactionActor", "wallet-transactions")
		_, err := newTestState(t, &stubResolver{err: resolveErr}).GetBulk(t.Context(), &api.GetBulkStateRequest{
			ActorType: "TransactionActor", ActorID: "1", Keys: []string{"a"},
		}, false)
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)
	})
}

func TestState_Transactional_ResolvesByActorType(t *testing.T) {
	upsert := func(key string) api.TransactionalOperation {
		return api.TransactionalOperation{
			Operation: api.Upsert,
			Request:   api.TransactionalUpsert{Key: key, Value: "v"},
		}
	}

	t.Run("writes to the resolved store", func(t *testing.T) {
		resolver := &stubResolver{name: "wallet-transactions", store: daprt.NewFakeStateStore()}
		err := newTestState(t, resolver).TransactionalStateOperation(t.Context(), true, &api.TransactionalRequest{
			ActorType: "TransactionActor", ActorID: "1", Operations: []api.TransactionalOperation{upsert("k")},
		}, false)
		require.NoError(t, err)
		assert.Equal(t, []string{"TransactionActor"}, resolver.seen)
		assert.Equal(t, uint64(1), resolver.store.CallCount("Multi"))
	})

	t.Run("applies the operation limit of the resolved store", func(t *testing.T) {
		store := daprt.NewFakeStateStore()
		store.MaxOperations = 1
		err := newTestState(t, &stubResolver{name: "wallet-transactions", store: store}).TransactionalStateOperation(t.Context(), true, &api.TransactionalRequest{
			ActorType: "TransactionActor", ActorID: "1", Operations: []api.TransactionalOperation{upsert("a"), upsert("b")},
		}, false)
		require.ErrorIs(t, err, ErrTransactionsTooManyOperations)
	})

	t.Run("returns the resolver error unchanged", func(t *testing.T) {
		resolveErr := messages.ErrActorStateStoreNotFound.WithFormat("TransactionActor", "wallet-transactions")
		err := newTestState(t, &stubResolver{err: resolveErr}).TransactionalStateOperation(t.Context(), true, &api.TransactionalRequest{
			ActorType: "TransactionActor", ActorID: "1", Operations: []api.TransactionalOperation{upsert("k")},
		}, false)
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)
	})

	// Characterization guard: a missing actor instance is reported before the
	// store is resolved, as it was before the resolver existed.
	t.Run("missing actor instance is checked before resolving", func(t *testing.T) {
		resolver := &stubResolver{err: messages.ErrActorStateStoreNotFound.WithFormat("TransactionActor", "wallet-transactions")}
		s := New(Options{
			AppID:         "app",
			StoreResolver: resolver,
			Resiliency:    resiliency.New(logger.NewLogger(t.Name())),
			Table:         tablefake.New().WithActorExists(func(string, string) bool { return false }),
		})
		err := s.TransactionalStateOperation(t.Context(), false, &api.TransactionalRequest{
			ActorType: "TransactionActor", ActorID: "1", Operations: []api.TransactionalOperation{upsert("k")},
		}, false)
		require.ErrorIs(t, err, messages.ErrActorInstanceMissing)
		assert.Empty(t, resolver.seen)
	})
}
