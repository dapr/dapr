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

	contribstate "github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

// fakeStores is a StoreGetter backed by a map. actorName names the store in
// the default actor state store slot; empty means the slot is empty.
type fakeStores struct {
	stores    map[string]contribstate.Store
	actorName string
}

func (f *fakeStores) GetStateStore(name string) (contribstate.Store, bool) {
	store, ok := f.stores[name]
	return store, ok
}

func (f *fakeStores) GetStateStoreActor() (contribstate.Store, string, bool) {
	if f.actorName == "" {
		return nil, "", false
	}
	store, ok := f.stores[f.actorName]
	return store, f.actorName, ok
}

func TestResolver_Unbound(t *testing.T) {
	t.Run("empty actor state store slot", func(t *testing.T) {
		r := NewResolver(ResolverOptions{Stores: &fakeStores{}})
		_, _, err := r.Resolve("BalanceActor")
		require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)
	})

	t.Run("capable default store", func(t *testing.T) {
		store := daprt.NewFakeStateStore()
		r := NewResolver(ResolverOptions{Stores: &fakeStores{
			stores:    map[string]contribstate.Store{"default": store},
			actorName: "default",
		}})
		name, got, err := r.Resolve("BalanceActor")
		require.NoError(t, err)
		assert.Equal(t, "default", name)
		assert.Same(t, store, got)
	})

	t.Run("default store without ETag and transactions", func(t *testing.T) {
		r := NewResolver(ResolverOptions{Stores: &fakeStores{
			stores:    map[string]contribstate.Store{"default": &daprt.MockStateStore{}},
			actorName: "default",
		}})
		_, _, err := r.Resolve("BalanceActor")
		require.EqualError(t, err, errStateStoreNotConfigured)
	})
}

func TestResolver_Bound(t *testing.T) {
	bindings := map[string]string{"TransactionActor": "wallet-transactions"}

	t.Run("resolves the bound store", func(t *testing.T) {
		bound := daprt.NewFakeStateStore()
		r := NewResolver(ResolverOptions{
			Bindings: bindings,
			Stores: &fakeStores{
				stores: map[string]contribstate.Store{
					"default":             daprt.NewFakeStateStore(),
					"wallet-transactions": bound,
				},
				actorName: "default",
			},
		})
		name, got, err := r.Resolve("TransactionActor")
		require.NoError(t, err)
		assert.Equal(t, "wallet-transactions", name)
		assert.Same(t, bound, got)
	})

	t.Run("bound store not loaded does not fall back to the default", func(t *testing.T) {
		r := NewResolver(ResolverOptions{
			Bindings: bindings,
			Stores: &fakeStores{
				stores:    map[string]contribstate.Store{"default": daprt.NewFakeStateStore()},
				actorName: "default",
			},
		})
		_, _, err := r.Resolve("TransactionActor")
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)
		assert.Contains(t, err.Error(), "TransactionActor")
		assert.Contains(t, err.Error(), "wallet-transactions")
		assert.NotContains(t, err.Error(), "actorStateStore")
	})

	t.Run("bound store does not need the default slot", func(t *testing.T) {
		r := NewResolver(ResolverOptions{
			Bindings: bindings,
			Stores: &fakeStores{
				stores: map[string]contribstate.Store{"wallet-transactions": daprt.NewFakeStateStore()},
			},
		})
		name, _, err := r.Resolve("TransactionActor")
		require.NoError(t, err)
		assert.Equal(t, "wallet-transactions", name)
	})

	t.Run("unbound type still uses the default", func(t *testing.T) {
		def := daprt.NewFakeStateStore()
		r := NewResolver(ResolverOptions{
			Bindings: bindings,
			Stores: &fakeStores{
				stores: map[string]contribstate.Store{
					"default":             def,
					"wallet-transactions": daprt.NewFakeStateStore(),
				},
				actorName: "default",
			},
		})
		name, got, err := r.Resolve("BalanceActor")
		require.NoError(t, err)
		assert.Equal(t, "default", name)
		assert.Same(t, def, got)
	})

	t.Run("binding may name the default actor state store", func(t *testing.T) {
		def := daprt.NewFakeStateStore()
		r := NewResolver(ResolverOptions{
			Bindings: map[string]string{"TransactionActor": "default"},
			Stores: &fakeStores{
				stores:    map[string]contribstate.Store{"default": def},
				actorName: "default",
			},
		})
		name, got, err := r.Resolve("TransactionActor")
		require.NoError(t, err)
		assert.Equal(t, "default", name)
		assert.Same(t, def, got)
	})

	t.Run("bindings are copied", func(t *testing.T) {
		b := map[string]string{"TransactionActor": "wallet-transactions"}
		r := NewResolver(ResolverOptions{
			Bindings: b,
			Stores: &fakeStores{
				stores:    map[string]contribstate.Store{"default": daprt.NewFakeStateStore()},
				actorName: "default",
			},
		})
		delete(b, "TransactionActor")
		_, _, err := r.Resolve("TransactionActor")
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)
	})
}

// TestResolver_CompStore verifies stores are resolved from the component
// store on every call, so hot reloading a store (add, replace, remove) is
// observed without reconstructing the resolver.
func TestResolver_CompStore(t *testing.T) {
	newStore := func() contribstate.Store {
		return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
	}

	t.Run("default actor state store", func(t *testing.T) {
		cs := compstore.New()
		r := NewResolver(ResolverOptions{Stores: cs})

		_, _, err := r.Resolve("PlainActor")
		require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)

		require.NoError(t, cs.AddStateStoreActor("mystore", newStore()))
		name, store, err := r.Resolve("PlainActor")
		require.NoError(t, err)
		assert.Equal(t, "mystore", name)
		assert.NotNil(t, store)

		cs.DeleteStateStore("mystore")
		_, _, err = r.Resolve("PlainActor")
		require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)

		// A store under a different name is picked up by name transparently.
		require.NoError(t, cs.AddStateStoreActor("otherstore", newStore()))
		name, _, err = r.Resolve("PlainActor")
		require.NoError(t, err)
		assert.Equal(t, "otherstore", name)
	})

	t.Run("bound state store", func(t *testing.T) {
		cs := compstore.New()
		require.NoError(t, cs.AddStateStoreActor("default", newStore()))
		r := NewResolver(ResolverOptions{
			Stores:   cs,
			Bindings: map[string]string{"TransactionActor": "wallet-transactions"},
		})

		_, _, err := r.Resolve("TransactionActor")
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)

		cs.AddStateStore("wallet-transactions", newStore())
		name, _, err := r.Resolve("TransactionActor")
		require.NoError(t, err)
		assert.Equal(t, "wallet-transactions", name)

		cs.DeleteStateStore("wallet-transactions")
		_, _, err = r.Resolve("TransactionActor")
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)

		// Unbound types are unaffected throughout.
		name, _, err = r.Resolve("PlainActor")
		require.NoError(t, err)
		assert.Equal(t, "default", name)
	})

	t.Run("binding to the default store follows its removal", func(t *testing.T) {
		cs := compstore.New()
		require.NoError(t, cs.AddStateStoreActor("default", newStore()))
		r := NewResolver(ResolverOptions{
			Stores:   cs,
			Bindings: map[string]string{"TransactionActor": "default"},
		})

		name, _, err := r.Resolve("TransactionActor")
		require.NoError(t, err)
		assert.Equal(t, "default", name)

		cs.DeleteStateStore("default")
		_, _, err = r.Resolve("TransactionActor")
		require.ErrorIs(t, err, messages.ErrActorStateStoreNotFound)
		_, _, err = r.Resolve("PlainActor")
		require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)
	})
}
