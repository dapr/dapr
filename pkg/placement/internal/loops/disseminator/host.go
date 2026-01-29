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
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/ptr"
)

func (d *disseminator) handleReportedHost(report *loops.ReportedHost) {
	//nolint:protogetter
	op := report.Host.Operation

	// TODO: @joshvanl: remove block in v1.18
	if report.Host.GetOperation() == v1pb.HostOperation_UNKNOWN {
		// If the reported host has changes, treat it as a report.
		if d.store.HostChanged(report.StreamIDx, report.Host) {
			op = v1pb.HostOperation_REPORT
		} else {
			if d.currentOperation == v1pb.HostOperation_REPORT {
				// Special case to ensure old clients become ready when batching a
				// dissemination.
				return
			}
			// Special case old clients- this always moves the lock forward.
			op = d.currentOperation
		}
	}

	//nolint:protogetter
	if report.Host.Version != nil && *report.Host.Version < d.currentVersion {
		log.Debugf("Ignoring report from stream %d - old version %d (current %d)", report.StreamIDx, *report.Host.Version, d.currentVersion)
		return
	}

	log.Debugf("Received report from stream (idx:%d) (op=%s) (ver=%d)", report.StreamIDx, op.String(), d.currentVersion)

	switch op {
	case v1pb.HostOperation_REPORT:
		d.doReport(report.StreamIDx, report.Host)

	case v1pb.HostOperation_UNLOCK:
		d.handleReportedUnlock(report.StreamIDx)

	case v1pb.HostOperation_LOCK:
		d.handleReportedLock(report.StreamIDx)

	case v1pb.HostOperation_UPDATE:
		d.handleReportedUpdate(report.StreamIDx)
	}
}

func (d *disseminator) doReport(streamIDx uint64, host *v1pb.Host) {
	if !d.store.Set(streamIDx, host) {
		log.Debugf("Ignoring report from stream %d - no changes", streamIDx)
		return
	}

	d.timeoutQ.Dequeue(d.currentVersion)
	d.currentVersion++
	d.timeoutQ.Enqueue(d.currentVersion)
	d.currentOperation = v1pb.HostOperation_LOCK

	for _, s := range d.streams {
		s.currentState = ptr.Of(v1pb.HostOperation_REPORT)
		s.loop.Enqueue(&loops.DisseminateLock{
			Version: d.currentVersion,
		})
	}
}

func (d *disseminator) handleReportedLock(streamIDx uint64) {
	stream, ok := d.streams[streamIDx]
	if !ok {
		return
	}

	stream.currentState = ptr.Of(v1pb.HostOperation_LOCK)
	stream.sentDoubleUnlock = 0

	if d.allStreamsHaveState(v1pb.HostOperation_LOCK) {
		// All streams have locked, move to update phase.
		d.currentOperation = v1pb.HostOperation_UPDATE

		for _, s := range d.streams {
			s.loop.Enqueue(&loops.DisseminateUpdate{
				Version: d.currentVersion,
				Tables:  d.store.PlacementTables(d.currentVersion),
			})
		}
	}
}

func (d *disseminator) handleReportedUpdate(streamIDx uint64) {
	stream, ok := d.streams[streamIDx]
	if !ok {
		return
	}

	stream.currentState = ptr.Of(v1pb.HostOperation_UPDATE)
	stream.sentDoubleUnlock = 0

	if d.allStreamsHaveState(v1pb.HostOperation_UPDATE) {
		// All streams have updated, dissemination is complete, send out unlocks.
		d.currentOperation = v1pb.HostOperation_UNLOCK

		for _, s := range d.streams {
			s.loop.Enqueue(&loops.DisseminateUnlock{
				Version: d.currentVersion,
			})
		}
	}
}

func (d *disseminator) handleReportedUnlock(streamIDx uint64) {
	stream, ok := d.streams[streamIDx]
	if !ok {
		return
	}

	stream.currentState = ptr.Of(v1pb.HostOperation_UNLOCK)
	stream.sentDoubleUnlock = 0

	if d.allStreamsHaveState(v1pb.HostOperation_UNLOCK) {
		d.currentOperation = v1pb.HostOperation_REPORT

		d.timeoutQ.Dequeue(d.currentVersion)
		log.Debugf("Dissemination of version %d complete", d.currentVersion)
	}
}

func (d *disseminator) allStreamsHaveState(state v1pb.HostOperation) bool {
	for _, stream := range d.streams {
		if stream.currentState == nil || *stream.currentState != state {
			return false
		}
	}
	return true
}
