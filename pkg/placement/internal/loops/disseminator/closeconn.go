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

package disseminator

import (
	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/stream"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// handleCloseStream handles a close stream request.
func (d *disseminator) handleCloseStream(closeStream *loops.ConnCloseStream) {
	s, ok := d.streams[closeStream.StreamIDx]
	if !ok {
		// Ignore old streams.
		return
	}

	monitoring.RecordRuntimesCount(d.connCount.Add(-1), d.namespace)
	if s.hasActors {
		monitoring.RecordActorRuntimesCount(d.actorConnCount.Add(-1), d.namespace)
	}

	delete(d.streams, closeStream.StreamIDx)
	s.loop.Close(&loops.StreamShutdown{
		Error: closeStream.Error,
	})
	stream.StreamLoopFactory.CacheLoop(s.loop)

	if len(d.streams) == 0 {
		return
	}

	if d.currentOperation != v1pb.HostOperation_REPORT {
		d.waitingToDelete = append(d.waitingToDelete, closeStream.StreamIDx)
		return
	}

	d.store.Delete(closeStream.StreamIDx)
	d.currentVersion++
	d.timeoutQ.Enqueue(d.currentVersion)
	d.currentOperation = v1pb.HostOperation_LOCK

	for _, s := range d.streams {
		s.currentState = v1pb.HostOperation_REPORT
		s.loop.Enqueue(&loops.DisseminateLock{
			Version: d.currentVersion,
		})
	}
}
