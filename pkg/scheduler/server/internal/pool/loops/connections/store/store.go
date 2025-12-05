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
	Loop       loop.Interface[loops.Event]
	AppID      *string
	ActorTypes []string
}

// TODO: sync.Pool
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
	//nolint:prealloc
	var fns []context.CancelFunc

	if opts.AppID != nil {
		fns = append(fns, s.appIDs.add(*opts.AppID, opts.Loop))
	}

	for _, actorType := range opts.ActorTypes {
		fns = append(fns, s.actorTypes.add(actorType, opts.Loop))
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

func (s *Store) AppID(id string) (loop.Interface[loops.Event], bool) {
	return s.appIDs.get(id)
}

func (s *Store) ActorType(actorType string) (loop.Interface[loops.Event], bool) {
	return s.actorTypes.get(actorType)
}
