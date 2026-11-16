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

// Package store holds the in-memory placement membership of a single
// namespace: which stream hosts which actor types. It builds the (partial)
// placement tables disseminated to sidecars and computes which actor types'
// tables change on membership updates.
package store

import (
	"slices"
	"sort"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type Store struct {
	// hosts are indexed on streamIDx.
	hosts map[uint64]*schedulerv1pb.ActorHost
}

func New() *Store {
	return &Store{
		hosts: make(map[uint64]*schedulerv1pb.ActorHost),
	}
}

// Set installs or updates the host reported by a stream. It returns the actor
// types whose placement table changed as a result: types the host started or
// stopped hosting, or every reported type when the host identity changed.
// Hosts with no actor types are not stored.
func (s *Store) Set(streamIDx uint64, host *schedulerv1pb.ActorHost) []string {
	//nolint:protogetter
	sort.Strings(host.ActorTypes)

	existing, ok := s.hosts[streamIDx]

	var changed []string
	switch {
	case !ok:
		changed = slices.Clone(host.GetActorTypes())
	case existing.GetAddress() != host.GetAddress() || existing.GetAppId() != host.GetAppId():
		changed = union(existing.GetActorTypes(), host.GetActorTypes())
	default:
		changed = symmetricDiff(existing.GetActorTypes(), host.GetActorTypes())
	}

	if len(host.GetActorTypes()) == 0 {
		delete(s.hosts, streamIDx)
	} else {
		s.hosts[streamIDx] = host
	}

	return changed
}

// Delete removes a stream's host, returning the actor types whose placement
// table changed.
func (s *Store) Delete(streamIDx uint64) []string {
	existing, ok := s.hosts[streamIDx]
	if !ok {
		return nil
	}

	delete(s.hosts, streamIDx)
	return slices.Clone(existing.GetActorTypes())
}

// Has returns whether the stream has a stored host.
func (s *Store) Has(streamIDx uint64) bool {
	_, ok := s.hosts[streamIDx]
	return ok
}

// Types returns every actor type currently hosted in the namespace.
func (s *Store) Types() []string {
	set := make(map[string]struct{})
	for _, host := range s.hosts {
		for _, t := range host.GetActorTypes() {
			set[t] = struct{}{}
		}
	}

	types := make([]string, 0, len(set))
	for t := range set {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// Tables builds the placement tables for the given actor types. A type with
// no remaining hosts gets an entry with an empty hosts map, which removes the
// type on the receiving side.
func (s *Store) Tables(types []string) *schedulerv1pb.PlacementTables {
	tables := &schedulerv1pb.PlacementTables{
		HashAlgorithm: schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS,
		Entries:       make(map[string]*schedulerv1pb.PlacementTable, len(types)),
	}

	for _, t := range types {
		tables.Entries[t] = &schedulerv1pb.PlacementTable{
			Hosts: make(map[string]*schedulerv1pb.PlacementHost),
		}
	}

	for _, host := range s.hosts {
		for _, t := range host.GetActorTypes() {
			entry, ok := tables.GetEntries()[t]
			if !ok {
				continue
			}
			entry.Hosts[host.GetAddress()] = &schedulerv1pb.PlacementHost{
				Address: host.GetAddress(),
				AppId:   host.GetAppId(),
			}
		}
	}

	return tables
}

// CollectOrphans appends orphaned store entry indices to the given slice. An
// orphan is a store entry whose streamIDx is not in the active set.
func (s *Store) CollectOrphans(isActive func(uint64) bool, orphans *[]uint64) {
	for idx := range s.hosts {
		if !isActive(idx) {
			*orphans = append(*orphans, idx)
		}
	}
}

// DeleteAll removes every host.
func (s *Store) DeleteAll() {
	clear(s.hosts)
}

// union returns the sorted union of two sorted string slices, deduplicated.
func union(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// symmetricDiff returns the elements present in exactly one of the two sorted
// string slices.
func symmetricDiff(a, b []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}
