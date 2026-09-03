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

	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
)

type Options struct {
	Loop       loop.Interface[loops.EventStream]
	AppID      *string
	ActorTypes []string

	// ActorAddress is the daprd internal gRPC host:port reported by the
	// stream, enabling actor reminder triggers to be routed to the placement
	// owner host. Nil for old daprds, which fall back to round robin.
	ActorAddress *string
}

type Store struct {
	appIDs     *instance
	actorTypes *instance
}

func New() *Store {
	return &Store{
		appIDs:     newInstance(),
		actorTypes: newInstance(),
	}
}

func (s *Store) Add(opts Options) context.CancelFunc {
	// We don't know how many allocations we will have!

	var fns []context.CancelFunc

	if opts.AppID != nil {
		fns = append(fns, s.appIDs.add(*opts.AppID, opts.Loop, nil))
	}

	for _, actorType := range opts.ActorTypes {
		fns = append(fns, s.actorTypes.add(actorType, opts.Loop, opts.ActorAddress))
	}

	monitoring.RecordSidecarsConnectedCount(1)
	return func() {
		for _, fn := range fns {
			fn()
		}

		opts.Loop.Close(new(loops.StreamShutdown))
		monitoring.RecordSidecarsConnectedCount(-1)
	}
}

func (s *Store) AppID(id string) (loop.Interface[loops.EventStream], bool) {
	return s.appIDs.get(id)
}

func (s *Store) ActorType(actorType string) (loop.Interface[loops.EventStream], bool) {
	return s.actorTypes.get(actorType)
}

// ActorHost returns a stream of the host owning the given actor ID per the
// rendezvous hash over the actor type's reported host addresses. Falls back
// to round robin when any host did not report an address.
func (s *Store) ActorHost(actorType, actorID string) (loop.Interface[loops.EventStream], bool) {
	return s.actorTypes.getByKey(actorType, actorID)
}
