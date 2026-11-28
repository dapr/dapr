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
	"fmt"
	"maps"
	"slices"

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

// BoundStore is a state store with the actor types bound to it.
type BoundStore struct {
	// Name is the name of the state store component.
	Name string
	// ActorTypes are the actor types bound to the store, sorted.
	ActorTypes []string
}

var (
	errBoundStoreNotLoaded = errors.New("the state store is not loaded")
	errBoundStoreFeatures  = errors.New("the state store does not support both the ETag and transactional features required for actor state")
)

// Resolver resolves the state store that holds an actor type's state.
type Resolver struct {
	stores      StoreGetter
	bindings    map[string]string
	boundStores []BoundStore
}

func NewResolver(opts ResolverOptions) *Resolver {
	typesOf := make(map[string][]string)
	for actorType, storeName := range opts.Bindings {
		typesOf[storeName] = append(typesOf[storeName], actorType)
	}
	boundStores := make([]BoundStore, 0, len(typesOf))
	for _, storeName := range slices.Sorted(maps.Keys(typesOf)) {
		slices.Sort(typesOf[storeName])
		boundStores = append(boundStores, BoundStore{Name: storeName, ActorTypes: typesOf[storeName]})
	}

	return &Resolver{
		stores:      opts.Stores,
		bindings:    maps.Clone(opts.Bindings),
		boundStores: boundStores,
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

// Unusable returns, for each bound actor type whose state store cannot be
// used, the error Resolve returns for it: the store is not loaded, or lacks
// the features actor state needs. Like Resolve, it looks the stores up on
// every call.
func (r *Resolver) Unusable() map[string]error {
	unusable := make(map[string]error)
	for actorType, storeName := range r.bindings {
		if _, _, err := r.resolveBound(actorType, storeName); err != nil {
			unusable[actorType] = err
		}
	}
	return unusable
}

// Bindings returns a copy of the bindings, from actor type to state store
// name.
func (r *Resolver) Bindings() map[string]string {
	return maps.Clone(r.bindings)
}

// BoundStores returns the state stores that actor types are bound to, sorted
// by name, each with its actor types, sorted. The bindings do not change, so
// it is built once. The caller must not modify it.
func (r *Resolver) BoundStores() []BoundStore {
	return r.boundStores
}

// CheckBoundStore returns why the named state store cannot hold the state of
// the actor types bound to it: it is not loaded, or lacks the features actor
// state needs. It returns nil if the store can be used. Like Resolve, it
// looks the store up on every call.
func (r *Resolver) CheckBoundStore(storeName string) error {
	storeS, ok := r.stores.GetStateStore(storeName)
	if !ok {
		return errBoundStoreNotLoaded
	}
	if _, ok := capable(storeS); !ok {
		return errBoundStoreFeatures
	}
	return nil
}

func (r *Resolver) resolveBound(actorType, storeName string) (string, Backend, error) {
	storeS, ok := r.stores.GetStateStore(storeName)
	if !ok {
		return "", nil, messages.ErrActorStateStoreNotFound.WithFormat(actorType, storeName)
	}

	store, ok := capable(storeS)
	if !ok {
		return "", nil, fmt.Errorf("actors: state store %q bound to actor type %q must support both the ETag and transactional features required for actor state", storeName, actorType)
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
