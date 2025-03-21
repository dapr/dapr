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

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/connection"
)

type store struct {
	appIDs     *instance
	actorTypes *instance
}

func newStore() *store {
	return &store{
		appIDs:     newInstance(),
		actorTypes: newInstance(),
	}
}

func (s *store) add(conn *connection.Connection, opts Options) context.CancelFunc {
	// We don't know how many allocations we will have!
	//nolint:prealloc
	var fns []context.CancelFunc

	if opts.AppID != nil {
		fns = append(fns, s.appIDs.add(*opts.AppID, conn))
	}

	for _, actorType := range opts.ActorTypes {
		fns = append(fns, s.actorTypes.add(actorType, conn))
	}

	return func() {
		for _, fn := range fns {
			fn()
		}
	}
}
