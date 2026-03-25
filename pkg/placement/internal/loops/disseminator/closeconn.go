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

	// If this stream was already counted towards the current dissemination
	// phase, decrement the counter so the round can still complete without it.
	// Without this, streamsInTargetState would exceed len(d.streams) after
	// deletion.
	if s.currentState == d.currentOperation && d.currentOperation != v1pb.HostOperation_REPORT {
		d.streamsInTargetState--
	}

	delete(d.streams, closeStream.StreamIDx)
	s.loop.Close(&loops.StreamShutdown{
		Error: closeStream.Error,
	})
	stream.StreamLoopFactory.CacheLoop(s.loop)

	if len(d.streams) == 0 {
		return
	}

	// Hosts with no entities were never stored, so there is nothing to remove
	// from the placement table.
	if !d.store.Has(closeStream.StreamIDx) {
		return
	}

	// Always queue the deletion rather than immediately starting a new
	// dissemination round. This allows rapid sequential disconnections (as
	// happen during a rolling update) to be batched together. The queued
	// deletions will be processed when the current dissemination round
	// completes, or when a new connection triggers a round via handleAdd.
	d.waitingToDelete = append(d.waitingToDelete, closeStream.StreamIDx)

	if d.currentOperation != v1pb.HostOperation_REPORT {
		// We're currently disseminating. The store deletion is queued above. Check
		// if removing this stream completes the current phase, this happens when
		// this was the last stream that hadn't responded yet.
		if d.streamsInTargetState >= len(d.streams) {
			d.advancePhase()
		}
		return
	}

	// In REPORT state, process accumulated deletions now. Multiple closes
	// arriving in rapid succession (during a rolling update) will be batched
	// naturally because handleAdd processes waitingToDelete before starting a
	// new round, combining deletes and adds into a single dissemination.
	d.processWaitingDeletes()
}

// advancePhase checks if all streams have reached the target state and
// advances to the next dissemination phase. This is called from
// handleCloseStream when removing a stream during active dissemination may
// have completed the current phase.
func (d *disseminator) advancePhase() {
	if d.streamsInTargetState < len(d.streams) {
		return
	}

	switch d.currentOperation {
	case v1pb.HostOperation_LOCK:
		d.currentOperation = v1pb.HostOperation_UPDATE
		d.streamsInTargetState = 0
		for _, s := range d.streams {
			s.loop.Enqueue(&loops.DisseminateUpdate{
				Version: d.currentVersion,
				Tables:  d.store.PlacementTables(d.currentVersion),
			})
		}

	case v1pb.HostOperation_UPDATE:
		d.currentOperation = v1pb.HostOperation_UNLOCK
		d.streamsInTargetState = 0
		for _, s := range d.streams {
			s.loop.Enqueue(&loops.DisseminateUnlock{
				Version: d.currentVersion,
			})
		}

	case v1pb.HostOperation_UNLOCK:
		d.currentOperation = v1pb.HostOperation_REPORT
		d.streamsInTargetState = 0
		d.timeoutQ.Dequeue(d.currentVersion)
		log.Debugf("Dissemination of version %d in %s complete (via stream close)", d.currentVersion, d.namespace)

		if len(d.waitingToDelete) > 0 {
			d.processWaitingDeletes()
		}
	}
}

// processWaitingDeletes processes all queued stream deletions in a single
// dissemination round. Must only be called when currentOperation is REPORT.
func (d *disseminator) processWaitingDeletes() {
	if len(d.waitingToDelete) == 0 || len(d.streams) == 0 {
		return
	}

	for _, toDelete := range d.waitingToDelete {
		d.store.Delete(toDelete)
	}
	d.waitingToDelete = nil

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
