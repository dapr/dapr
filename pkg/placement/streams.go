package placement

import (
	"sync"
	"sync/atomic"
)

// streamConnPool has the stream connections established between the placement gRPC server
// and the Dapr runtime grouped by namespace, with an assigned id for faster lookup/deletion
// The id is a simple auto-incrementing number, for efficiency.
type streamConnPool struct {
	lock sync.RWMutex

	// Example representation of streams
	//	streamConnPool = {
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
	streams map[string]map[uint32]daprdStream

	// streamIndexCnt assigns an index to streams in the streamConnPool.
	// Its reset to zero every time a placement service loses leadership (thus clears all streams).
	streamIndexCnt atomic.Uint32
}

// add adds stream connection between runtime and placement to the namespaced dissemination pool.
func (s *streamConnPool) add(stream daprdStream) {
	s.lock.Lock()
	id := s.streamIndexCnt.Add(1)
	stream.id = id
	s.streams[stream.namespace][id] = stream
	s.lock.Unlock()
}

// delete removes stream connection between runtime and placement from the namespaced dissemination pool.
func (s *streamConnPool) delete(stream daprdStream) {
	s.lock.Lock()

	if streams, ok := s.streams[stream.namespace]; ok {
		delete(streams, stream.id)
		if len(streams) == 0 {
			delete(s.streams, stream.namespace)
		}
	}
	s.lock.Unlock()
}

func newStreamConnPool() *streamConnPool {
	return &streamConnPool{
		streams: make(map[string]map[uint32]daprdStream),
	}
}
