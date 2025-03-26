/*
Copyright 2025 The Dapr Authors
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

package store

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/connection"
)

type Options struct {
	Namespace  string
	AppID      *string
	ActorTypes []string
}

// Namespace is a pool of connections for multiple Namespaces. Each Namespace
// has a pool of connections for AppID and ActorType.
type Namespace struct {
	lock   sync.RWMutex
	stores map[string]*store
}

func New() *Namespace {
	return &Namespace{
		stores: make(map[string]*store),
	}
}

func (n *Namespace) Add(ctx context.Context, opts Options) (context.Context, *connection.Connection, context.CancelFunc) {
	n.lock.Lock()
	defer n.lock.Unlock()

	conn := connection.New()

	store, ok := n.stores[opts.Namespace]
	if !ok {
		store = newStore()
		n.stores[opts.Namespace] = store
	}

	ctx, cancel := context.WithCancel(ctx)
	remove := store.add(conn, opts)

	return ctx, conn, func() {
		n.lock.Lock()
		defer n.lock.Unlock()
		cancel()
		remove()
		conn.Close()

		if len(store.appIDs.entries) == 0 &&
			len(store.actorTypes.entries) == 0 {
			delete(n.stores, opts.Namespace)
		}
	}
}

func (n *Namespace) AppID(namespace, id string) (*connection.Connection, bool) {
	n.lock.RLock()
	defer n.lock.RUnlock()

	store, ok := n.stores[namespace]
	if !ok {
		return nil, false
	}

	return store.appIDs.get(id)
}

func (n *Namespace) ActorType(namespace, actorType string) (*connection.Connection, bool) {
	n.lock.RLock()
	defer n.lock.RUnlock()

	store, ok := n.stores[namespace]
	if !ok {
		return nil, false
	}

	conn, ok := store.actorTypes.get(actorType)
	return conn, ok
}
