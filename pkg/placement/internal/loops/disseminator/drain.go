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

// handleDrain runs this namespace's final dissemination round with an
// emptied store, so the delivered table is empty and every sidecar halts
// its actors. Round completion, timeout, or the last stream disconnecting
// then closes the remaining streams and reports DrainComplete.
func (d *disseminator) handleDrain(e *loops.Drain) {
	if d.draining {
		return
	}
	d.draining = true
	d.drainError = e.Error

	d.stopCoalesceTimer()

	// Streams queued for a future round would never receive the empty
	// table, so refuse them outright.
	for _, wait := range d.waitingToDisseminate {
		wait.Cancel(e.Error)
	}
	d.waitingToDisseminate = nil
	d.waitingToDelete = nil

	d.store.DeleteAll()

	if len(d.streams) == 0 {
		d.finishDrain()
		return
	}

	// Start the final round over the emptied store, superseding any round in
	// flight the same way the timeout restart does.
	d.timeoutQ.Dequeue(d.currentVersion)
	d.currentVersion++
	d.timeoutQ.Enqueue(d.currentVersion)
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streamsInTargetState = 0
	for _, s := range d.streams {
		s.currentState = v1pb.HostOperation_REPORT
		s.receivingTable = nil
		s.loop.Enqueue(&loops.DisseminateLock{
			Version: d.currentVersion,
		})
	}
}

// finishDrain closes every remaining stream and reports DrainComplete.
// Idempotent: the drain can finish through round completion, timeout, or the
// last stream disconnecting.
func (d *disseminator) finishDrain() {
	if d.drainDone {
		return
	}
	d.drainDone = true

	d.timeoutQ.Dequeue(d.currentVersion)

	for idx, s := range d.streams {
		go func(s *streamConn) {
			s.loop.Close(&loops.StreamShutdown{
				Error: d.drainError,
			})
			stream.StreamLoopFactory.CacheLoop(s.loop)
		}(s)
		delete(d.streams, idx)
	}

	d.connCount.Store(0)
	d.actorConnCount.Store(0)
	monitoring.RecordRuntimesCount(0, d.namespace)
	monitoring.RecordActorRuntimesCount(0, d.namespace)

	d.nsLoop.Enqueue(&loops.DrainComplete{
		Namespace: d.namespace,
	})
}
