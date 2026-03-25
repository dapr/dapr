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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator/store"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator/timeout"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/events/loop/fake"
)

func newTestDisseminator(t *testing.T) *disseminator {
	t.Helper()

	dissLoop := fake.New[loops.EventDisseminator]()

	d := &disseminator{
		nsLoop:           fake.New[loops.EventNamespace](),
		loop:             dissLoop,
		timeout:          5 * time.Second,
		namespace:        "default",
		streams:          make(map[uint64]*streamConn),
		store:            store.New(store.Options{ReplicationFactor: 1}),
		currentOperation: v1pb.HostOperation_REPORT,
		timeoutQ: timeout.New(timeout.Options{
			Loop:    dissLoop,
			Timeout: 5 * time.Second,
		}),
	}

	t.Cleanup(func() { d.timeoutQ.Close() })

	return d
}

type fakeStreamLoop struct {
	loop     *fake.Fake[loops.EventStream]
	enqueued atomic.Int32
	closeCh  chan struct{}
}

func addFakeStream(d *disseminator, idx uint64, entities []string) *fakeStreamLoop {
	fs := &fakeStreamLoop{closeCh: make(chan struct{}, 1)}
	fs.loop = fake.New[loops.EventStream]().
		WithEnqueue(func(loops.EventStream) { fs.enqueued.Add(1) }).
		WithClose(func(loops.EventStream) {
			select {
			case fs.closeCh <- struct{}{}:
			default:
			}
		})

	d.streams[idx] = &streamConn{
		loop:         fs.loop,
		currentState: v1pb.HostOperation_REPORT,
		hasActors:    len(entities) > 0,
	}
	if len(entities) > 0 {
		//nolint:gosec
		d.store.Set(idx, &v1pb.Host{
			Name:      "app-" + string(rune('0'+idx)),
			Id:        "app-" + string(rune('0'+idx)),
			Namespace: "default",
			Entities:  entities,
		})
	}
	d.connCount.Add(1)
	if len(entities) > 0 {
		d.actorConnCount.Add(1)
	}
	if idx >= d.streamIDx {
		d.streamIDx = idx + 1
	}
	return fs
}

func host(id string, entities ...string) *v1pb.Host {
	return &v1pb.Host{
		Name:      id,
		Id:        id,
		Namespace: "default",
		Entities:  entities,
	}
}

func TestHandleTimeout_IgnoresStaleVersion(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 5

	d.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 3})

	assert.Len(t, d.streams, 1)
	assert.True(t, d.store.Has(0))
}

func TestHandleTimeout_ClosesOnlyNonRespondingStreams(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streams[0].currentState = v1pb.HostOperation_LOCK
	d.streams[1].currentState = v1pb.HostOperation_REPORT

	d.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 1})

	assert.Contains(t, d.streams, uint64(0))
	assert.NotContains(t, d.streams, uint64(1))
	assert.False(t, d.store.Has(1))
	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
	assert.Equal(t, uint64(2), d.currentVersion)
}

func TestHandleTimeout_AllStreamsRemoved_NoStreamsLeft(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streams[0].currentState = v1pb.HostOperation_REPORT

	d.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 1})

	assert.Empty(t, d.streams)
	assert.Equal(t, v1pb.HostOperation_REPORT, d.currentOperation)
}

func TestHandleTimeout_WaitingConnectionsSurvive(t *testing.T) {
	t.Skip("requires real gRPC stream for addStream — covered by integration test waitingcancel")
}

func TestHandleTimeout_ProcessesWaitingDeletes(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streams[0].currentState = v1pb.HostOperation_LOCK
	d.streams[1].currentState = v1pb.HostOperation_REPORT
	d.waitingToDelete = []uint64{99}
	d.store.Set(99, host("departed", "actorA"))

	d.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 1})

	assert.False(t, d.store.Has(99))
	assert.Nil(t, d.waitingToDelete)
}

func TestHandleCloseStream_RemovesFromStreams(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})

	d.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})

	assert.NotContains(t, d.streams, uint64(0))
	assert.Equal(t, int64(0), d.connCount.Load())
}

func TestHandleCloseStream_QueuesDeleteDuringRound(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentOperation = v1pb.HostOperation_LOCK

	d.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})

	assert.NotContains(t, d.streams, uint64(0))
	assert.Contains(t, d.waitingToDelete, uint64(0))
	assert.True(t, d.store.Has(0))
}

func TestHandleCloseStream_ProcessesImmediatelyInReportState(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentOperation = v1pb.HostOperation_REPORT

	d.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})

	assert.False(t, d.store.Has(0))
	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
}

func TestHandleCloseStream_DecrementsTargetState(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streamsInTargetState = 1
	d.streams[0].currentState = v1pb.HostOperation_LOCK

	d.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})

	assert.Equal(t, 0, d.streamsInTargetState)
}

func TestHandleCloseStream_AdvancesPhaseWhenLastNonResponder(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streamsInTargetState = 1
	d.streams[0].currentState = v1pb.HostOperation_LOCK
	d.streams[1].currentState = v1pb.HostOperation_REPORT

	d.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 1, Namespace: "default"})

	assert.Equal(t, v1pb.HostOperation_UPDATE, d.currentOperation)
}

func TestHandleCloseStream_NoActorsSkipsDissemination(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, nil)
	addFakeStream(d, 1, []string{"actorA"})

	d.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})

	assert.Equal(t, v1pb.HostOperation_REPORT, d.currentOperation)
}

func TestHandleCloseStream_IgnoresUnknownStream(t *testing.T) {
	d := newTestDisseminator(t)
	d.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 999, Namespace: "default"})
}

func TestAdvancePhase_LockToUpdate(t *testing.T) {
	d := newTestDisseminator(t)
	fakeLoop := addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streamsInTargetState = 1

	d.advancePhase()

	assert.Equal(t, v1pb.HostOperation_UPDATE, d.currentOperation)
	assert.Equal(t, 0, d.streamsInTargetState)
	assert.Equal(t, int32(1), fakeLoop.enqueued.Load())
}

func TestAdvancePhase_UpdateToUnlock(t *testing.T) {
	d := newTestDisseminator(t)
	fakeLoop := addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_UPDATE
	d.streamsInTargetState = 1

	d.advancePhase()

	assert.Equal(t, v1pb.HostOperation_UNLOCK, d.currentOperation)
	assert.Equal(t, 0, d.streamsInTargetState)
	assert.Equal(t, int32(1), fakeLoop.enqueued.Load())
}

func TestAdvancePhase_UnlockToReport(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_UNLOCK
	d.streamsInTargetState = 1

	d.advancePhase()

	assert.Equal(t, v1pb.HostOperation_REPORT, d.currentOperation)
}

func TestAdvancePhase_UnlockProcessesWaitingDeletes(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_UNLOCK
	d.streamsInTargetState = 1
	d.waitingToDelete = []uint64{99}
	d.store.Set(99, host("gone", "actorA"))

	d.advancePhase()

	assert.False(t, d.store.Has(99))
	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
}

func TestAdvancePhase_NoAdvanceIfNotAllResponded(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK
	d.streamsInTargetState = 1

	d.advancePhase()

	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
}

func TestHandleAdd_QueuesDuringActiveRound(t *testing.T) {
	d := newTestDisseminator(t)
	d.currentOperation = v1pb.HostOperation_LOCK

	d.handleAdd(t.Context(), &loops.ConnAdd{
		InitialHost: host("queued-app", "actorA"),
		Cancel:      func(error) {},
	})

	assert.Len(t, d.waitingToDisseminate, 1)
	assert.Empty(t, d.streams)
}

func TestHandleAdd_ProcessesPendingDeletesFirst(t *testing.T) {
	t.Skip("requires real gRPC stream for addStream — covered by integration tests")
}

func TestProcessWaitingDeletes_DeletesFromStoreAndStartsRound(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.store.Set(99, host("old", "actorA"))
	d.waitingToDelete = []uint64{99}

	d.processWaitingDeletes()

	assert.False(t, d.store.Has(99))
	assert.Nil(t, d.waitingToDelete)
	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
}

func TestProcessWaitingDeletes_NoopIfEmpty(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})

	d.processWaitingDeletes()

	assert.Equal(t, v1pb.HostOperation_REPORT, d.currentOperation)
}

func TestProcessWaitingDeletes_NoopIfNoStreams(t *testing.T) {
	d := newTestDisseminator(t)
	d.store.Set(99, host("old", "actorA"))
	d.waitingToDelete = []uint64{99}

	d.processWaitingDeletes()

	assert.Equal(t, v1pb.HostOperation_REPORT, d.currentOperation)
}

func TestReportedLock_AdvancesToUpdate(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK

	d.handleReportedLock(0)

	assert.Equal(t, v1pb.HostOperation_UPDATE, d.currentOperation)
	assert.Equal(t, 0, d.streamsInTargetState)
}

func TestReportedLock_WaitsForAllStreams(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	addFakeStream(d, 1, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_LOCK

	d.handleReportedLock(0)

	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
	assert.Equal(t, 1, d.streamsInTargetState)
}

func TestReportedUpdate_AdvancesToUnlock(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_UPDATE

	d.handleReportedUpdate(0)

	assert.Equal(t, v1pb.HostOperation_UNLOCK, d.currentOperation)
}

func TestReportedUnlock_CompletesRound(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_UNLOCK

	d.handleReportedUnlock(t.Context(), 0)

	assert.Equal(t, v1pb.HostOperation_REPORT, d.currentOperation)
}

func TestReportedUnlock_ProcessesWaitingDeletes(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_UNLOCK
	d.waitingToDelete = []uint64{99}
	d.store.Set(99, host("departed", "actorA"))

	d.handleReportedUnlock(t.Context(), 0)

	assert.False(t, d.store.Has(99))
	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
}

func TestReportedUnlock_CollectsOrphans(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})
	d.currentVersion = 1
	d.currentOperation = v1pb.HostOperation_UNLOCK
	d.store.Set(99, host("orphan", "actorA"))

	d.handleReportedUnlock(t.Context(), 0)

	assert.False(t, d.store.Has(99))
	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
}

func TestDoReport_NoStoreChange_NoRound(t *testing.T) {
	d := newTestDisseminator(t)
	addFakeStream(d, 0, []string{"actorA"})

	d.doReport(0, host("app-0", "actorA"))

	assert.Equal(t, v1pb.HostOperation_REPORT, d.currentOperation)
}

func TestDoReport_StoreChange_StartsLockRound(t *testing.T) {
	d := newTestDisseminator(t)
	fakeLoop := addFakeStream(d, 0, []string{"actorA"})

	d.doReport(0, host("app-0", "actorA", "actorB"))

	assert.Equal(t, v1pb.HostOperation_LOCK, d.currentOperation)
	assert.Equal(t, uint64(1), d.currentVersion)
	assert.Equal(t, int32(1), fakeLoop.enqueued.Load())
}

func TestStoreCollectOrphans(t *testing.T) {
	s := store.New(store.Options{ReplicationFactor: 1})
	s.Set(0, host("alive", "actorA"))
	s.Set(1, host("orphan", "actorA"))
	s.Set(2, host("also-alive", "actorA"))

	activeStreams := map[uint64]bool{0: true, 2: true}

	var orphans []uint64
	s.CollectOrphans(func(idx uint64) bool {
		return activeStreams[idx]
	}, &orphans)

	assert.Equal(t, []uint64{uint64(1)}, orphans)
}

func TestStoreCollectOrphans_NoOrphans(t *testing.T) {
	s := store.New(store.Options{ReplicationFactor: 1})
	s.Set(0, host("alive", "actorA"))

	activeStreams := map[uint64]bool{0: true}

	var orphans []uint64
	s.CollectOrphans(func(idx uint64) bool {
		return activeStreams[idx]
	}, &orphans)

	assert.Empty(t, orphans)
}
