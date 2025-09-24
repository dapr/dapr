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

	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/stream"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.store")

type StreamConnection struct {
	Loop   loop.Interface[loops.Event]
	Cancel context.CancelCauseFunc
}

type Options struct {
	Connection *StreamConnection
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

func (n *Namespace) Add(opts Options) context.CancelCauseFunc {
	n.lock.Lock()
	defer n.lock.Unlock()

	store, ok := n.stores[opts.Namespace]
	if !ok {
		store = newStore()
		n.stores[opts.Namespace] = store
	}

	remove := store.add(opts.Connection, opts)
	monitoring.RecordSidecarsConnectedCount(1)
	return func(err error) {
		monitoring.RecordSidecarsConnectedCount(-1)
		n.lock.Lock()
		defer n.lock.Unlock()
		var appID string
		if opts.AppID != nil {
			appID = *opts.AppID
		}
		log.Debugf("Closing connection to %s [appID=%q] [actorTypes=%v]", opts.Namespace, appID, opts.ActorTypes)

		opts.Connection.Cancel(err)
		remove()
		opts.Connection.Loop.Close(new(loops.StreamShutdown))
		stream.StreamLoopCache.Put(opts.Connection.Loop)

		if len(store.appIDs.entries) == 0 &&
			len(store.actorTypes.entries) == 0 {
			delete(n.stores, opts.Namespace)
		}
		log.Debugf("Closed connection to %s [appID=%v] [actorTypes=%v]", opts.Namespace, opts.AppID, opts.ActorTypes)
	}
}

func (n *Namespace) AppID(namespace, id string) (loop.Interface[loops.Event], bool) {
	n.lock.RLock()
	defer n.lock.RUnlock()

	store, ok := n.stores[namespace]
	if !ok {
		return nil, false
	}

	return store.appIDs.get(id)
}

func (n *Namespace) ActorType(namespace, actorType string) (loop.Interface[loops.Event], bool) {
	n.lock.RLock()
	defer n.lock.RUnlock()

	store, ok := n.stores[namespace]
	if !ok {
		return nil, false
	}

	return store.actorTypes.get(actorType)
}
