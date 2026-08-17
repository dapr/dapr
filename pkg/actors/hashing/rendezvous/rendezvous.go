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

// Package rendezvous implements rendezvous (highest random weight) hashing
// over a set of host addresses. It is the PlacementV2 actor placement
// algorithm, used by both the daprd sidecar and the scheduler so that all
// parties resolve the same owner host for a given actor ID from the same host
// set. Compared to a vnode consistent hash ring it has optimal balance, needs
// no replication factor tuning, and moves the provably minimal set of keys
// when a host joins or leaves.
package rendezvous

import (
	"slices"

	"github.com/cespare/xxhash/v2"
)

// separator matches the wire contract documented on
// dapr.proto.scheduler.v1.HashAlgorithm: the score of a (host, key) pair is
// xxhash64("<host address>||<key>").
const separator = "||"

// Table is an immutable rendezvous hash table over a set of host addresses.
type Table struct {
	hosts []string
	// prefixes[i] is a Digest pre-fed with hosts[i] and the separator.
	// Lookup streams only the key into a copy of it.
	prefixes []xxhash.Digest
}

// New returns a table over the given host addresses. Duplicates are removed
// and the input slice is not retained.
func New(hosts []string) *Table {
	sorted := slices.Clone(hosts)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	prefixes := make([]xxhash.Digest, len(sorted))
	for i, host := range sorted {
		prefixes[i].Reset()
		//nolint:errcheck // Digest.WriteString never returns an error.
		prefixes[i].WriteString(host)
		//nolint:errcheck
		prefixes[i].WriteString(separator)
	}

	return &Table{hosts: sorted, prefixes: prefixes}
}

// Lookup returns the host address owning the given key, false when the table
// has no hosts. The owner is the host with the highest xxhash64 score for the
// key. Score ties break to the lexicographically smaller host address so that
// every party resolves the same owner.
func (t *Table) Lookup(key string) (string, bool) {
	if t == nil || len(t.hosts) == 0 {
		return "", false
	}

	owner := t.hosts[0]
	best := t.score(0, key)
	// hosts are sorted, so on a tie the earlier (smaller) host wins by never
	// replacing on equal score.
	for i := 1; i < len(t.hosts); i++ {
		if s := t.score(i, key); s > best {
			owner, best = t.hosts[i], s
		}
	}

	return owner, true
}

// score is xxhash64("<host>||<key>") for hosts[i], resumed from the host's
// precomputed prefix digest.
func (t *Table) score(i int, key string) uint64 {
	d := t.prefixes[i]
	//nolint:errcheck // Digest.WriteString never returns an error.
	d.WriteString(key)
	return d.Sum64()
}

// Hosts returns the sorted host addresses in the table. The returned slice
// must not be mutated.
func (t *Table) Hosts() []string {
	if t == nil {
		return nil
	}
	return t.hosts
}

// Equal returns whether both tables are over the same host set.
func (t *Table) Equal(other *Table) bool {
	if t == nil || other == nil {
		return t == other || len(t.Hosts()) == len(other.Hosts())
	}
	return slices.Equal(t.hosts, other.hosts)
}

func score(host, key string) uint64 {
	var d xxhash.Digest
	d.Reset()
	//nolint:errcheck // Digest.WriteString never returns an error.
	d.WriteString(host)
	//nolint:errcheck
	d.WriteString(separator)
	//nolint:errcheck
	d.WriteString(key)
	return d.Sum64()
}
