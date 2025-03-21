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

type entry struct {
	idx   uint64
	conns []*connection.Connection
}

type instance struct {
	entries map[string]*entry
}

func newInstance() *instance {
	return &instance{
		entries: make(map[string]*entry),
	}
}

func (i *instance) add(name string, conn *connection.Connection) context.CancelFunc {
	en, ok := i.entries[name]
	if !ok {
		en = new(entry)
		i.entries[name] = en
	}

	en.conns = append(en.conns, conn)
	return func() {
		for idx, c := range en.conns {
			if c == conn {
				en.conns = append(en.conns[:idx], en.conns[idx+1:]...)
				break
			}
		}

		if len(en.conns) == 0 {
			delete(i.entries, name)
		}
	}
}

func (i *instance) get(name string) (*connection.Connection, bool) {
	en, ok := i.entries[name]
	if !ok {
		return nil, false
	}

	// Increase index to load balance over connections for this instance.
	defer func() { en.idx++ }()
	return en.conns[en.idx%uint64(len(en.conns))], true
}
