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
	"errors"
	"maps"

	contribstate "github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/messages"
)

// StoreGetter looks up loaded state stores. It is satisfied by
// *compstore.ComponentStore.
type StoreGetter interface {
	GetStateStore(name string) (contribstate.Store, bool)
	GetStateStoreActor() (contribstate.Store, string, bool)
}

type ResolverOptions struct {
	// Stores looks up the loaded state stores.
	Stores StoreGetter

	// Bindings maps an actor type to the name of the state store bound to it.
	Bindings map[string]string
}

// Resolver resolves the state store that holds an actor type's state.
type Resolver struct {
	stores   StoreGetter
	bindings map[string]string
}

func NewResolver(opts ResolverOptions) *Resolver {
	return &Resolver{
		stores:   opts.Stores,
		bindings: maps.Clone(opts.Bindings),
	}
}

// Resolve returns the name of the state store for the given actor type, and
// the store itself. An actor type bound to a state store uses only that
// store, and never falls back to the default actor state store. Other actor
// types use the default actor state store. Stores are looked up on every
// call, so they can be hot reloaded (added, replaced, or removed) at any
// point in the runtime's lifetime.
func (r *Resolver) Resolve(actorType string) (string, Backend, error) {
	if storeName, ok := r.bindings[actorType]; ok {
		return r.resolveBound(actorType, storeName)
	}
	return r.resolveDefault()
}

func (r *Resolver) resolveBound(actorType, storeName string) (string, Backend, error) {
	storeS, ok := r.stores.GetStateStore(storeName)
	if !ok {
		return "", nil, messages.ErrActorStateStoreNotFound.WithFormat(actorType, storeName)
	}

	store, ok := capable(storeS)
	if !ok {
		return "", nil, errors.New(errStateStoreNotConfigured)
	}

	return storeName, store, nil
}

func (r *Resolver) resolveDefault() (string, Backend, error) {
	storeS, storeName, ok := r.stores.GetStateStoreActor()
	if !ok {
		return "", nil, messages.ErrActorRuntimeNotFound
	}

	store, ok := capable(storeS)
	if !ok {
		return "", nil, errors.New(errStateStoreNotConfigured)
	}

	return storeName, store, nil
}

// capable returns the store as a Backend if it supports both ETags and
// transactions, which the actor runtime needs for every state operation.
func capable(storeS contribstate.Store) (Backend, bool) {
	store, ok := storeS.(Backend)
	if !ok || !contribstate.FeatureETag.IsPresent(store.Features()) || !contribstate.FeatureTransactional.IsPresent(store.Features()) {
		return nil, false
	}
	return store, true
}
