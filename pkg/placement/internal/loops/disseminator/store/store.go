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

package store

import (
	"slices"
	"sort"
	"strconv"

	"google.golang.org/protobuf/proto"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

type Options struct {
	ReplicationFactor int64
}

type Store struct {
	replicationFactor int64

	// hosts are indexed on streamIDx.
	hosts map[uint64]*v1pb.Host
}

func New(opts Options) *Store {
	return &Store{
		replicationFactor: opts.ReplicationFactor,
		hosts:             make(map[uint64]*v1pb.Host),
	}
}

func (s *Store) PlacementTables(version uint64) *v1pb.PlacementTables {
	t := &v1pb.PlacementTables{
		ReplicationFactor: s.replicationFactor,
		Entries:           make(map[string]*v1pb.PlacementTable),
		Version:           strconv.FormatUint(version, 10),
	}

	for _, host := range s.hosts {
		for _, entity := range host.GetEntities() {
			if e := t.GetEntries(); e[entity] == nil {
				e[entity] = &v1pb.PlacementTable{
					LoadMap: make(map[string]*v1pb.Host),
				}
			}

			//nolint:protogetter
			t.Entries[entity].LoadMap[host.Name] = host
		}
	}

	return t
}

func (s *Store) StatePlacementTable(version uint64) *v1pb.StatePlacementTable {
	hosts := make([]*v1pb.Host, len(s.hosts))
	var i int
	for _, host := range s.hosts {
		hosts[i] = proto.Clone(host).(*v1pb.Host)
		i++
	}

	sort.SliceStable(hosts, func(i, j int) bool {
		return hosts[i].GetName() < hosts[j].GetName()
	})

	return &v1pb.StatePlacementTable{
		Version: version,
		Hosts:   hosts,
	}
}

func (s *Store) Set(streamIDx uint64, host *v1pb.Host) bool {
	if !s.HostChanged(streamIDx, host) {
		return false
	}

	s.hosts[streamIDx] = host
	return true
}

func (s *Store) HostChanged(streamIDx uint64, host *v1pb.Host) bool {
	//nolint:protogetter
	sort.Strings(host.Entities)
	existing, ok := s.hosts[streamIDx]
	if !ok {
		return true
	}

	return !(slices.Equal(existing.GetEntities(), host.GetEntities()) &&
		host.GetId() == existing.GetId() &&
		host.GetNamespace() == existing.GetNamespace())
}

func (s *Store) Delete(streamIDx uint64) {
	delete(s.hosts, streamIDx)
}

func (s *Store) DeleteAll() {
	clear(s.hosts)
}
