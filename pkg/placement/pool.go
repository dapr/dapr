/*
Copyright 2024 The Dapr Authors
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

package placement

import (
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/metadata"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

type daprdStream struct {
	id            uint32
	hostName      string
	hostID        string
	hostNamespace string
	needsVNodes   bool
	stream        placementv1pb.Placement_ReportDaprStatusServer
}

func newDaprdStream(host *placementv1pb.Host, stream placementv1pb.Placement_ReportDaprStatusServer) *daprdStream {
	return &daprdStream{
		hostID:        host.GetId(),
		hostName:      host.GetName(),
		hostNamespace: host.GetNamespace(),
		stream:        stream,
		needsVNodes:   hostNeedsVNodes(stream),
	}
}

// streamConnPool has the stream connections established between the placement gRPC server
// and the Dapr runtime grouped by namespace, with an assigned id for faster lookup/deletion
// The id is a simple auto-incrementing number, for efficiency.
type streamConnPool struct {
	// locks the streams map itself
	lock sync.RWMutex

	streams map[string]map[uint32]*daprdStream

	// streamIndex assigns an index to streams in the streamConnPool.
	// Its reset to zero every time a placement service loses leadership (thus clears all streams).
	streamIndex atomic.Uint32

	// reverseLookup is a reverse index of streams to their daprdStream object.
	// (so we don't have to loop through all streams to find a specific one)
	reverseLookup map[placementv1pb.Placement_ReportDaprStatusServer]*daprdStream
}

func newStreamConnPool() *streamConnPool {
	return &streamConnPool{
		streams:       make(map[string]map[uint32]*daprdStream),
		reverseLookup: make(map[placementv1pb.Placement_ReportDaprStatusServer]*daprdStream),
	}
}

// add adds stream connection between runtime and placement to the namespaced dissemination pool.
func (s *streamConnPool) add(stream *daprdStream) {
	id := s.streamIndex.Add(1)
	stream.id = id

	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.streams[stream.hostNamespace]; !ok {
		s.streams[stream.hostNamespace] = make(map[uint32]*daprdStream)
	}

	s.streams[stream.hostNamespace][id] = stream
	s.reverseLookup[stream.stream] = stream
}

// delete removes a stream connection between runtime and placement
// from the namespaced dissemination pool.
// Returns true if the stream is the last one in a namespace
func (s *streamConnPool) delete(stream *daprdStream) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if streams, ok := s.streams[stream.hostNamespace]; ok {
		delete(streams, stream.id)
		delete(s.reverseLookup, stream.stream)
		if len(streams) == 0 {
			delete(s.streams, stream.hostNamespace)
		}
	}
}

func (s *streamConnPool) getStreamCount(namespace string) int {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return len(s.streams[namespace])
}

func (s *streamConnPool) forEachNamespace(fn func(namespace string, val map[uint32]*daprdStream)) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for ns, val := range s.streams {
		fn(ns, val)
	}
}

func (s *streamConnPool) forEachInNamespace(namespace string, fn func(key uint32, val *daprdStream)) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for key, val := range s.streams[namespace] {
		fn(key, val)
	}
}

func (s *streamConnPool) getStream(stream placementv1pb.Placement_ReportDaprStatusServer) (*daprdStream, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	daprdStream, ok := s.reverseLookup[stream]
	return daprdStream, ok
}

func hostNeedsVNodes(stream placementv1pb.Placement_ReportDaprStatusServer) bool {
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		// default to older versions that need vnodes
		return true
	}

	// Extract apiLevel from metadata
	vmd := md.Get(GRPCContextKeyAcceptVNodes)
	return !(len(vmd) > 0 && strings.EqualFold(vmd[0], "false"))
}
