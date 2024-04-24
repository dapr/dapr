package placement

import (
	"strings"
	"sync/atomic"

	"google.golang.org/grpc/metadata"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/concurrency"
)

type placementGRPCStream placementv1pb.Placement_ReportDaprStatusServer //nolint:nosnakecase

type daprdStream struct {
	id          uint32
	namespace   string
	needsVNodes bool
	stream      placementGRPCStream
}

func newDaprdStream(ns string, stream placementGRPCStream) daprdStream {
	return daprdStream{
		namespace:   ns,
		stream:      stream,
		needsVNodes: hostNeedsVNodes(stream),
	}
}

// streamConnPool has the stream connections established between the placement gRPC server
// and the Dapr runtime grouped by namespace, with an assigned id for faster lookup/deletion
// The id is a simple auto-incrementing number, for efficiency.
type streamConnPool struct {
	mutexMap *concurrency.MutexMap

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
	streams map[string]map[uint32]daprdStream

	// streamIndexCnt assigns an index to streams in the streamConnPool.
	// Its reset to zero every time a placement service loses leadership (thus clears all streams).
	streamIndexCnt atomic.Uint32
}

// add adds stream connection between runtime and placement to the namespaced dissemination pool.
func (s *streamConnPool) add(stream daprdStream) {
	s.mutexMap.Lock(stream.namespace)
	defer s.mutexMap.Unlock(stream.namespace)

	id := s.streamIndexCnt.Add(1)
	stream.id = id

	if _, ok := s.streams[stream.namespace]; !ok {
		s.streams[stream.namespace] = make(map[uint32]daprdStream)
	}

	s.streams[stream.namespace][id] = stream
}

// delete removes stream connection between runtime and placement from the namespaced dissemination pool.
func (s *streamConnPool) delete(stream daprdStream) {
	s.mutexMap.Lock(stream.namespace)
	defer s.mutexMap.Unlock(stream.namespace)

	if streams, ok := s.streams[stream.namespace]; ok {
		delete(streams, stream.id)
		if len(streams) == 0 {
			delete(s.streams, stream.namespace)
			s.mutexMap.Delete(stream.namespace, false)
		}
	}
}

// getStreams requires a lock (s.lock) to be held by the caller.
func (s *streamConnPool) getStreams(namespace string) map[uint32]daprdStream {
	return s.streams[namespace]
}

func newStreamConnPool() *streamConnPool {
	return &streamConnPool{
		mutexMap: &concurrency.MutexMap{},
		streams:  make(map[string]map[uint32]daprdStream),
	}
}

func (s *streamConnPool) hasStream(id uint32) bool {
	s.mutexMap.OuterRLock()
	defer s.mutexMap.OuterRUnlock()

	for ns, streams := range s.streams {
		s.mutexMap.RLock(ns)
		if _, ok := streams[id]; ok {
			s.mutexMap.RUnlock(ns)
			return true
		}
		s.mutexMap.RUnlock(ns)
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
