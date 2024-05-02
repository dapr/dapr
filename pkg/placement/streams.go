package placement

import (
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/metadata"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

type placementGRPCStream placementv1pb.Placement_ReportDaprStatusServer //nolint:nosnakecase

type daprdStream struct {
	id          uint32
	namespace   string
	needsVNodes bool
	stream      placementGRPCStream
}

func newDaprdStream(ns string, stream placementGRPCStream) *daprdStream {
	return &daprdStream{
		namespace:   ns,
		stream:      stream,
		needsVNodes: hostNeedsVNodes(stream),
	}
}

// streamConnPool has the stream connections established between the placement gRPC server
// and the Dapr runtime grouped by namespace, with an assigned id for faster lookup/deletion
// The id is a simple auto-incrementing number, for efficiency.
type streamConnPool struct {
	lock sync.RWMutex // locks the streams map itself
	//mutexMap *concurrency.MutexMap // manages locks on elements in the streams map

	// Example representation of streams
	//	{
	//	  "ns1": {
	//	  	1: stream1,
	//	 	2: stream2,
	//			3: stream3,
	//		},
	//
	//	"ns2": {
	//	  	4: stream4,
	//	 	5: stream5,
	//		},
	//	}
	streams map[string]map[uint32]*daprdStream

	// streamIndexCnt assigns an index to streams in the streamConnPool.
	// Its reset to zero every time a placement service loses leadership (thus clears all streams).
	streamIndexCnt atomic.Uint32
}

// add adds stream connection between runtime and placement to the namespaced dissemination pool.
func (s *streamConnPool) add(stream *daprdStream) {
	id := s.streamIndexCnt.Add(1)
	stream.id = id

	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.streams[stream.namespace]; !ok {
		s.streams[stream.namespace] = make(map[uint32]*daprdStream)
	}

	s.streams[stream.namespace][id] = stream
}

// delete removes stream connection between runtime and placement from the namespaced dissemination pool.
func (s *streamConnPool) delete(stream *daprdStream) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if streams, ok := s.streams[stream.namespace]; ok {
		delete(streams, stream.id)
		if len(streams) == 0 {
			delete(s.streams, stream.namespace)
		}
	}

	// TODO: @elena Return true or false if it's the last stream in the namespace. If last stream,
	// we need to delete elements from service.memberUpdateCount and service.disseminateNextTime
}

// getStreams requires a lock (s.lock) to be held by the caller.
func (s *streamConnPool) getStreams(namespace string) map[uint32]*daprdStream {
	return s.streams[namespace]
}

func (s *streamConnPool) getStreamCount(namespace string) int {
	s.lock.RLock()
	defer s.lock.RUnlock()
	streams, ok := s.streams[namespace]
	if !ok {
		return 0
	}

	return len(streams)
}

func (s *streamConnPool) forEachNamespace(fn func(namespace string, val map[uint32]*daprdStream)) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for ns, val := range s.streams {
		fn(ns, val)
	}
}

func (s *streamConnPool) forEach(namespace string, fn func(key uint32, val *daprdStream)) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for key, val := range s.streams[namespace] {
		fn(key, val)
	}
}

func newStreamConnPool() *streamConnPool {
	return &streamConnPool{
		streams: make(map[string]map[uint32]*daprdStream),
	}
}

func (s *streamConnPool) hasStream(id uint32) bool {
	s.lock.RLock()
	defer s.lock.RUnlock()

	for _, streams := range s.streams {
		if _, ok := streams[id]; ok {
			return true
		}
	}
	return false
}

func hostNeedsVNodes(stream placementGRPCStream) bool {
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return true // default to older versions that need vnodes
	}

	// Extract apiLevel from metadata
	vmd := md.Get(GRPCContextKeyAcceptVNodes)
	return !(len(vmd) > 0 && strings.EqualFold(vmd[0], "false"))
}
