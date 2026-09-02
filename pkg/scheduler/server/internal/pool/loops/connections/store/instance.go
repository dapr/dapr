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

	"github.com/dapr/dapr/pkg/actors/hashing/rendezvous"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
)

// hostEntry are the streams of a single daprd host, identified by its actor
// address. One host contributes multiple streams.
type hostEntry struct {
	nextConn uint64
	conns    []loop.Interface[loops.EventStream]
}

// TODO: sync.Pool
type entry struct {
	nextConn uint64
	conns    []loop.Interface[loops.EventStream]

	// hosts groups this entry's streams by their reported actor address, and
	// table is the rendezvous hash over those addresses. Used to route actor
	// reminder triggers directly to the placement owner host. addrless
	// counts streams which did not report an address (old daprds): while any
	// exist, routing falls back to round robin over every stream so
	// addressless hosts are not starved.
	hosts    map[string]*hostEntry
	addrless int
	table    *rendezvous.Table
}

// TODO: sync.Pool
type instance struct {
	entries map[string]*entry
}

func newInstance() *instance {
	return &instance{
		entries: make(map[string]*entry),
	}
}

func (i *instance) add(name string, conn loop.Interface[loops.EventStream], address *string) context.CancelFunc {
	en, ok := i.entries[name]
	if !ok {
		en = &entry{
			hosts: make(map[string]*hostEntry),
		}
		i.entries[name] = en
	}

	en.conns = append(en.conns, conn)

	if address == nil || *address == "" {
		en.addrless++
		return func() {
			en.removeConn(conn)
			en.addrless--
			if len(en.conns) == 0 {
				delete(i.entries, name)
			}
		}
	}

	addr := *address
	he, ok := en.hosts[addr]
	if !ok {
		he = new(hostEntry)
		en.hosts[addr] = he
		en.rebuildTable()
	}
	he.conns = append(he.conns, conn)

	return func() {
		en.removeConn(conn)
		for idx, c := range he.conns {
			if c == conn {
				he.conns = append(he.conns[:idx], he.conns[idx+1:]...)
				break
			}
		}
		if len(he.conns) == 0 {
			delete(en.hosts, addr)
			en.rebuildTable()
		}
		if len(en.conns) == 0 {
			delete(i.entries, name)
		}
	}
}

func (e *entry) removeConn(conn loop.Interface[loops.EventStream]) {
	for idx, c := range e.conns {
		if c == conn {
			e.conns = append(e.conns[:idx], e.conns[idx+1:]...)
			break
		}
	}
}

func (e *entry) rebuildTable() {
	addrs := make([]string, 0, len(e.hosts))
	for addr := range e.hosts {
		addrs = append(addrs, addr)
	}
	e.table = rendezvous.New(addrs)
}

// roundRobin load balances over every stream of this entry.
func (e *entry) roundRobin() loop.Interface[loops.EventStream] {
	l := e.conns[e.nextConn%uint64(len(e.conns))]
	// Increase index to load balance over connections for this instance.
	e.nextConn++
	return l
}

func (i *instance) get(name string) (loop.Interface[loops.EventStream], bool) {
	en, ok := i.entries[name]
	if !ok {
		return nil, false
	}

	return en.roundRobin(), true
}

// getByKey returns a stream of the host owning the given key per the
// rendezvous hash over the entry's host addresses, round robining over that
// host's streams. Falls back to round robin over every stream when any
// stream is addressless or on any anomaly.
func (i *instance) getByKey(name, key string) (loop.Interface[loops.EventStream], bool) {
	en, ok := i.entries[name]
	if !ok {
		return nil, false
	}

	if en.addrless > 0 || len(en.hosts) == 0 {
		return en.roundRobin(), true
	}

	owner, ok := en.table.Lookup(key)
	if !ok {
		return en.roundRobin(), true
	}

	he, ok := en.hosts[owner]
	if !ok || len(he.conns) == 0 {
		return en.roundRobin(), true
	}

	l := he.conns[he.nextConn%uint64(len(he.conns))]
	he.nextConn++
	return l, true
}
