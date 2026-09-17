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

package connections

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

const pullActorType = "dapr.internal.default.myapp.activity"

// pullHarness is a connections loop with n pull-eligible sidecars, one stream
// each, recording what each stream was handed. Events the loop enqueues to
// itself are queued and applied by pump on the test goroutine.
type pullHarness struct {
	c          *connections
	events     chan loops.EventConn
	dispatched [][]*loops.TriggerRequest
	results    map[*loops.TriggerRequest]api.TriggerResponseResult
}

func newPullHarness(t *testing.T, streams int, slots uint32) *pullHarness {
	t.Helper()

	h := &pullHarness{
		c:          connsCache.New().(*connections),
		events:     make(chan loops.EventConn, 1024),
		dispatched: make([][]*loops.TriggerRequest, streams),
		results:    make(map[*loops.TriggerRequest]api.TriggerResponseResult),
	}
	h.c.schedulerCount = 1
	h.c.reattachGrace = time.Hour
	h.c.nsLoop = loopfake.New[loops.EventNS]()
	h.c.loop = loopfake.New[loops.EventConn]().WithEnqueue(func(e loops.EventConn) {
		h.events <- e
	})

	for i := range streams {
		h.addStream(t, i, slots)
	}
	return h
}

// pump applies every event the loop has enqueued to itself.
func (h *pullHarness) pump(t *testing.T) {
	t.Helper()
	for {
		select {
		case e := <-h.events:
			require.NoError(t, h.c.Handle(context.Background(), e))
		default:
			return
		}
	}
}

// addr is the actor address of the sidecar behind stream i; each index is
// its own sidecar unless a test connects a second stream with the same
// address.
func addr(i int) string {
	return "127.0.0.1:" + strconv.Itoa(1000+i)
}

func (h *pullHarness) pool(i int) *pullPool {
	return h.c.pullPoolOf(pullActorType, addr(i))
}

// addStream connects a stream recording into dispatched[i] for its own
// sidecar, offering slots.
func (h *pullHarness) addStream(t *testing.T, i int, slots uint32) uint64 {
	t.Helper()
	return h.addStreamFor(t, i, addr(i), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots: []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: slots}},
	})
}

// addStreamFor connects a stream recording into dispatched[i] for the
// sidecar at actorAddress with the given initial request, mirroring what
// handleAdd does after the stream loop is created.
func (h *pullHarness) addStreamFor(t *testing.T, i int, actorAddress string, req *schedulerv1pb.WatchJobsRequestInitial) uint64 {
	t.Helper()
	idx := h.c.streamIDx
	h.c.streamIDx++

	req.ActorTypes = []string{pullActorType}
	req.ActorAddress = &actorAddress
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		h.dispatched[i] = append(h.dispatched[i], e.(*loops.TriggerRequest))
	})
	h.c.streams[idx] = h.c.streamPool.Add(store.Options{
		Loop:         streamLoop,
		ActorTypes:   req.GetActorTypes(),
		ActorAddress: &actorAddress,
	})
	h.c.streamLoops[idx] = streamLoop
	h.c.updateConcurrencyLimits(idx, req)
	h.c.addPullStream(idx, req)
	h.c.drainPullPending()
	return idx
}

// closeStream closes stream idx the way the stream loop and connections loop
// do: the connections loop reaps the stream, then the stream orphans the pull
// triggers it still had in flight.
func (h *pullHarness) closeStream(t *testing.T, idx uint64, inflight ...*loops.TriggerRequest) {
	t.Helper()
	h.c.streams[idx] = func() {}
	h.c.handleCloseStream(&loops.ConnCloseStream{StreamIDx: idx, Namespace: "default"})
	for _, req := range inflight {
		req.Orphan()
	}
	h.pump(t)
}

func (h *pullHarness) trigger(actorID, name string) *loops.TriggerRequest {
	req := makeTriggerRequest(pullActorType, actorID, name)
	req.Job.Name = "run-activity"
	req.Job.Metadata.Pull = new(true)
	req.Job.Metadata.Namespace = "default"
	req.Job.Metadata.AppId = "myapp"
	req.ResultFn = func(r api.TriggerResponseResult) { h.results[req] = r }
	return req
}

// ack completes a dispatched trigger the way the stream loop does when the
// sidecar reports the result. It does not free a slot: only a report does.
func (h *pullHarness) ack(t *testing.T, req *loops.TriggerRequest) {
	t.Helper()
	req.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
}

// report delivers a free slot count reported by sidecar i.
func (h *pullHarness) report(i int, free uint32) {
	h.c.handlePullSlotsUpdate(&loops.PullSlotsUpdate{Sidecar: addr(i), ActorType: pullActorType, Free: free})
}

func (h *pullHarness) totalDispatched() int {
	n := 0
	for _, d := range h.dispatched {
		n += len(d)
	}
	return n
}

func ref(actorID string) *schedulerv1pb.ActorReminderRef {
	return &schedulerv1pb.ActorReminderRef{ActorType: pullActorType, ActorId: actorID, Name: "run-activity"}
}

func TestPullDispatch_FillsFreeSlotsAcrossSidecars(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 3, 1)

	for i := range 3 {
		h.c.handleTriggerRequest(h.trigger("wf-a::"+strconv.Itoa(i)+"::0", "act"))
	}

	for i := range 3 {
		assert.Len(t, h.dispatched[i], 1, "each single-slot sidecar takes exactly one trigger")
		assert.Equal(t, uint32(0), h.pool(i).credit)
	}
}

func TestPullDispatch_PrefersMostFreeSlots(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 4)
	h.report(0, 1)

	for i := range 3 {
		h.c.handleTriggerRequest(h.trigger("wf::"+strconv.Itoa(i)+"::0", "act"))
	}
	assert.Empty(t, h.dispatched[0])
	assert.Len(t, h.dispatched[1], 3)

	// Now both have one free slot: the next two go one each.
	h.c.handleTriggerRequest(h.trigger("wf::4::0", "act"))
	h.c.handleTriggerRequest(h.trigger("wf::5::0", "act"))
	assert.Len(t, h.dispatched[0], 1)
	assert.Len(t, h.dispatched[1], 4)
}

func TestPullDispatch_CreditComesFromReportsOnly(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 1)

	first := h.trigger("wf::1::0", "act")
	second := h.trigger("wf::2::0", "act")
	third := h.trigger("wf::3::0", "act")
	h.c.handleTriggerRequest(first)
	h.c.handleTriggerRequest(second)
	h.c.handleTriggerRequest(third)

	assert.Equal(t, 2, h.totalDispatched())
	require.Contains(t, h.c.pullPending, pullActorType)
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len())
	assert.NotContains(t, h.results, third, "a parked trigger has no result yet")

	h.ack(t, first)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.results[first])
	assert.Equal(t, 2, h.totalDispatched(), "a completion alone frees nothing: the sidecar may still be full")

	h.report(1, 0)
	assert.Equal(t, 2, h.totalDispatched())

	h.report(0, 1)
	assert.Equal(t, 3, h.totalDispatched(), "the reported slot takes the parked trigger")
	assert.Same(t, third, h.dispatched[0][1])
	assert.NotContains(t, h.c.pullPending, pullActorType, "the emptied queue is dropped")
}

func TestPullDispatch_SharedGateFullParks(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 5)

	// A global per-name gate of 1 for "act" shared by both sidecars.
	h.c.concurrencyGates[pullActorType+":act"] = &concurrencyGate{globalLimit: 1}
	h.c.streamGateKeys[0] = append(h.c.streamGateKeys[0], pullActorType+":act")

	first := h.trigger("wf::1::0", "act")
	second := h.trigger("wf::2::0", "act")
	other := h.trigger("wf::3::0", "other")
	h.c.handleTriggerRequest(first)
	h.c.handleTriggerRequest(second)
	h.c.handleTriggerRequest(other)

	assert.Equal(t, 2, h.totalDispatched(), "the second 'act' waits on the per-name gate; 'other' is unaffected")
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len())
	assert.Equal(t, uint32(8), h.pool(0).credit+h.pool(1).credit, "a failed shared-gate acquire takes no slot")

	h.ack(t, first)
	assert.Equal(t, 3, h.totalDispatched())
	assert.Equal(t, uint32(1), h.c.concurrencyGates[pullActorType+":act"].current)
}

func TestPullDispatch_RoundRobinsAcrossInstances(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	running := h.trigger("busy::0::0", "act")
	h.c.handleTriggerRequest(running)
	require.Equal(t, 1, h.totalDispatched())

	// Instance A fans out five tasks, then instance B schedules one.
	a := make([]*loops.TriggerRequest, 0, 5)
	for i := range 5 {
		req := h.trigger("wf-a::"+strconv.Itoa(i)+"::0", "act")
		a = append(a, req)
		h.c.handleTriggerRequest(req)
	}
	b := h.trigger("wf-b::1::0", "act")
	h.c.handleTriggerRequest(b)
	require.Equal(t, 6, h.c.pullPending[pullActorType].len())

	// Each completion frees the single slot; the drain must alternate A, B
	// rather than serving all of A first.
	finish := func(req *loops.TriggerRequest) {
		h.ack(t, req)
		h.report(0, 1)
	}
	finish(running)
	require.Len(t, h.dispatched[0], 2)
	assert.Same(t, a[0], h.dispatched[0][1])

	finish(a[0])
	require.Len(t, h.dispatched[0], 3)
	assert.Same(t, b, h.dispatched[0][2], "instance B is served before A's second task")

	finish(b)
	require.Len(t, h.dispatched[0], 4)
	assert.Same(t, a[1], h.dispatched[0][3])
}

func TestPullDispatch_FallsBackWithoutSlotStreams(t *testing.T) {
	t.Parallel()

	var dispatched []loops.EventStream
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		dispatched = append(dispatched, e)
	})
	c := connsCache.New().(*connections)
	c.schedulerCount = 1
	c.streamPool.Add(store.Options{Loop: streamLoop, ActorTypes: []string{pullActorType}})

	req := makeTriggerRequest(pullActorType, "wf::1::0", "act")
	req.Job.Metadata.Pull = new(true)
	c.handleTriggerRequest(req)

	require.Len(t, dispatched, 1, "a pull trigger with no sidecar reporting slots uses the regular actor routing")
	assert.Same(t, req, dispatched[0].(*loops.TriggerRequest))
	assert.Nil(t, req.Orphan, "a regularly routed trigger is not reattachable")
	assert.False(t, req.Job.GetMetadata().GetPull(), "the receiving sidecar must route it like any reminder")
	assert.Empty(t, c.pullPending)
}

func TestPullDispatch_NonPullTriggersIgnoreSlots(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	plain := makeTriggerRequest(pullActorType, "wf::1::0", "act")
	plain.ResultFn = func(api.TriggerResponseResult) {}
	h.c.handleTriggerRequest(plain)
	h.c.handleTriggerRequest(h.trigger("wf::2::0", "act"))

	assert.Len(t, h.dispatched[0], 2)
	assert.Equal(t, uint32(0), h.pool(0).credit, "only the pull trigger took the slot")
}

func TestPullDispatch_NoCapacityReparks(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 1)
	h.c.concurrencyGates[pullActorType] = &concurrencyGate{globalLimit: 10}
	h.c.streamGateKeys[0] = append(h.c.streamGateKeys[0], pullActorType)
	h.report(1, 0)

	req := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(req)
	require.Len(t, h.dispatched[0], 1)
	require.Equal(t, uint32(1), h.c.concurrencyGates[pullActorType].current)

	// Another scheduler filled the sidecar's last slot first.
	req.Repark()
	h.pump(t)

	assert.NotContains(t, h.results, req, "a refused trigger is not resolved to the cron")
	assert.Equal(t, uint32(0), h.pool(0).credit, "the refusing sidecar is full until it reports otherwise")
	assert.Equal(t, uint32(0), h.c.concurrencyGates[pullActorType].current, "the shared gate is released")
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len())
	assert.Empty(t, h.c.pullSidecars[addr(0)].flights)

	// The hooks of the refused request are spent.
	req.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
	assert.NotContains(t, h.results, req)

	h.report(1, 1)
	require.Len(t, h.dispatched[1], 1, "the parked trigger goes to the sidecar that reports a slot")
	again := h.dispatched[1][0]
	assert.Same(t, req.Job, again.Job)
	again.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.results[req], "the cron callback travels with the requeued trigger")
}

func TestPullDispatch_LastStreamClosingReturnsPending(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	parked := h.trigger("wf::2::0", "act")
	h.c.handleTriggerRequest(running)
	h.c.handleTriggerRequest(parked)
	require.Equal(t, 1, h.c.pullPending[pullActorType].len())

	h.closeStream(t, 0, running)

	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results[parked], "parked triggers go back to the cron for retry")
	assert.NotContains(t, h.results, running, "the running trigger waits for its sidecar to reattach")
	assert.Empty(t, h.c.pullPending)
	assert.Empty(t, h.c.pullPools)
	f := h.c.pullSidecars[addr(0)].flights[reminderKey(pullActorType, "wf::1::0", "run-activity")]
	require.NotNil(t, f)
	assert.True(t, f.orphaned)
	assert.NotNil(t, f.timer)
}

func TestPullDispatch_NewStreamDrainsPending(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	h.c.handleTriggerRequest(h.trigger("wf::1::0", "act"))
	parked := h.trigger("wf::2::0", "act")
	h.c.handleTriggerRequest(parked)
	require.Equal(t, 1, h.c.pullPending[pullActorType].len())

	h.dispatched = append(h.dispatched, nil)
	h.addStream(t, 1, 1)

	require.Len(t, h.dispatched[1], 1, "a newly connected sidecar with slots takes the parked trigger")
	assert.Same(t, parked, h.dispatched[1][0])
}

func TestPullDispatch_ShutdownReturnsPendingAndInflight(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)
	h.c.streams[0] = func() {}

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	parked := h.trigger("wf::2::0", "act")
	h.c.handleTriggerRequest(parked)

	h.c.handleShutdown()
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results[parked])
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results[running])
}

func TestPullDispatch_SameSidecarStreamsShareSlots(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	// The same sidecar reconnects (actor types changed) and, for a while,
	// holds two streams. They share the sidecar's slots.
	h.dispatched = append(h.dispatched, nil)
	second := h.addStreamFor(t, 1, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots: []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: 1}},
	})
	require.Len(t, h.c.pullPools[pullActorType], 1, "one pool per sidecar, not per stream")
	assert.Equal(t, []uint64{0, second}, h.pool(0).streams)
	assert.Equal(t, 2, h.c.pullSidecars[addr(0)].streams)

	first := h.trigger("wf::1::0", "act")
	parked := h.trigger("wf::2::0", "act")
	h.c.handleTriggerRequest(first)
	h.c.handleTriggerRequest(parked)

	assert.Empty(t, h.dispatched[0], "delivery prefers the sidecar's newest stream")
	assert.Len(t, h.dispatched[1], 1)
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len(), "the second trigger waits for the sidecar's only slot")

	// The old stream closing does not remove the sidecar or its slots.
	h.closeStream(t, 0)
	require.Len(t, h.c.pullPools[pullActorType], 1)
	assert.Equal(t, []uint64{second}, h.pool(0).streams)
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len())

	h.ack(t, first)
	h.report(0, 1)
	assert.Len(t, h.dispatched[1], 2)
	assert.Same(t, parked, h.dispatched[1][1])
}

func TestPullDispatch_ReattachAfterReconnect(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 1)
	h.report(1, 0)

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	require.Len(t, h.dispatched[0], 1)
	key := reminderKey(pullActorType, "wf::1::0", "run-activity")

	h.closeStream(t, 0, running)
	f := h.c.pullSidecars[addr(0)].flights[key]
	require.NotNil(t, f)
	require.NotNil(t, f.timer, "an orphaned flight waits a bounded time for its sidecar")

	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: 0}},
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})
	assert.True(t, f.confirmed)
	assert.Nil(t, f.timer, "a sidecar that lists the reminder keeps it without a deadline")
	h.report(1, 1)
	assert.Empty(t, h.dispatched[1], "the reattached trigger is not delivered elsewhere")

	h.c.handlePullResult(&loops.PullResult{Sidecar: addr(0), Reminder: ref("wf::1::0"), Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS})
	h.pump(t)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.results[running])
	assert.NotContains(t, h.c.pullSidecars[addr(0)].flights, key)
}

func TestPullDispatch_ConfirmedBeforeOrphaned(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)

	// The sidecar's new stream arrives before this scheduler notices the old
	// one closing.
	h.dispatched = append(h.dispatched, nil)
	h.addStreamFor(t, 1, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})
	h.closeStream(t, 0, running)

	f := h.c.pullSidecars[addr(0)].flights[reminderKey(pullActorType, "wf::1::0", "run-activity")]
	require.NotNil(t, f)
	assert.True(t, f.orphaned)
	assert.Nil(t, f.timer, "a confirmed flight on a live sidecar has no deadline")

	// The sidecar's last stream closes: the deadline starts again.
	h.closeStream(t, 1)
	assert.False(t, f.confirmed)
	assert.NotNil(t, f.timer)
}

func TestPullDispatch_UnlistedOrphanStartsTimer(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	h.closeStream(t, 0, running)
	f := h.c.pullSidecars[addr(0)].flights[reminderKey(pullActorType, "wf::1::0", "run-activity")]

	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})
	require.Nil(t, f.timer)

	// A later stream of the sidecar no longer lists it: it finished and its
	// result is on the way, or the sidecar lost it.
	h.dispatched = append(h.dispatched, nil)
	h.addStreamFor(t, 1, addr(0), &schedulerv1pb.WatchJobsRequestInitial{})
	assert.False(t, f.confirmed)
	assert.NotNil(t, f.timer)
}

func TestPullDispatch_OrphanExpires(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 1)
	h.c.reattachGrace = time.Millisecond
	h.report(1, 0)

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	require.Len(t, h.dispatched[0], 1)
	h.closeStream(t, 0, running)

	select {
	case e := <-h.events:
		require.NoError(t, h.c.Handle(context.Background(), e))
	case <-time.After(5 * time.Second):
		require.Fail(t, "the reattach timer did not fire")
	}
	h.pump(t)

	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results[running], "an unclaimed orphan goes back to the cron")
	assert.NotContains(t, h.c.pullSidecars, addr(0), "a sidecar with no streams and no flights is forgotten")
}

func TestPullDispatch_ResultForUnknownReminderIsDropped(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	h.c.handlePullResult(&loops.PullResult{Sidecar: addr(0), Reminder: ref("wf::9::0"), Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS})
	h.c.handlePullResult(&loops.PullResult{Sidecar: "other", Reminder: ref("wf::1::0"), Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS})
	h.pump(t)
	assert.Empty(t, h.results)
}

func TestPullDispatch_ReportsApplyPerSidecar(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 2)
	h.c.handleTriggerRequest(h.trigger("wf::1::0", "act"))
	require.Equal(t, uint32(1), h.pool(0).credit)

	h.c.handlePullSlotsUpdate(&loops.PullSlotsUpdate{Sidecar: "unknown", ActorType: pullActorType, Free: 9})
	h.c.handlePullSlotsUpdate(&loops.PullSlotsUpdate{Sidecar: addr(0), ActorType: "other", Free: 9})
	assert.Equal(t, uint32(1), h.pool(0).credit)
	h.report(0, 3)
	assert.Equal(t, uint32(3), h.pool(0).credit)
}

func TestPullQueue_InstanceKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "wf-1", pullInstanceKey("wf-1::3::0"))
	assert.Equal(t, "a::b", pullInstanceKey("a::b::3::0"), "instance IDs may contain the separator")
	assert.Equal(t, "plain", pullInstanceKey("plain"))
	assert.Equal(t, "one::two", pullInstanceKey("one::two"), "fewer than two separators is not an activity ID")
}

func TestPullQueue_TryDispatchOneRotates(t *testing.T) {
	t.Parallel()
	q := newPullQueue()

	mk := func(id string) *loops.TriggerRequest {
		return &loops.TriggerRequest{Job: &internalsv1pb.JobEvent{Metadata: &schedulerv1pb.JobMetadata{
			Target: &schedulerv1pb.JobTargetMetadata{Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: pullActorType, Id: id},
			}},
		}}}
	}
	a1, a2, a3, b1, c1 := mk("a::1::0"), mk("a::2::0"), mk("a::3::0"), mk("b::1::0"), mk("c::1::0")
	for _, r := range []*loops.TriggerRequest{a1, a2, a3, b1, c1} {
		require.True(t, q.enqueue(r))
	}
	require.Equal(t, 5, q.len())

	var order []*loops.TriggerRequest
	accept := func(r *loops.TriggerRequest) bool {
		order = append(order, r)
		return true
	}
	for q.tryDispatchOne(accept) {
	}
	assert.Equal(t, []*loops.TriggerRequest{a1, b1, c1, a2, a3}, order)
	assert.Equal(t, 0, q.len())
	assert.Empty(t, q.ring)

	// A rejected head is skipped for this rotation; another instance's head
	// may still go.
	require.True(t, q.enqueue(a1))
	require.True(t, q.enqueue(b1))
	order = nil
	require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
		order = append(order, r)
		return r == b1
	}))
	assert.Equal(t, []*loops.TriggerRequest{a1, b1}, order)
	assert.Equal(t, 1, q.len())
	assert.False(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return false }))
	assert.Equal(t, 1, q.len())
}

func TestPullQueue_DrainAll(t *testing.T) {
	t.Parallel()
	q := newPullQueue()
	req := makeTriggerRequest(pullActorType, "wf::1::0", "act")
	require.True(t, q.enqueue(req))
	var drained []*loops.TriggerRequest
	q.drainAll(func(r *loops.TriggerRequest) { drained = append(drained, r) })
	assert.Equal(t, []*loops.TriggerRequest{req}, drained)
	assert.Equal(t, 0, q.len())
}

func TestAddPullStream_Eligibility(t *testing.T) {
	t.Parallel()
	c := connsCache.New().(*connections)
	addr := "10.0.0.1:50001"

	c.addPullStream(7, &schedulerv1pb.WatchJobsRequestInitial{
		ActorTypes:   []string{pullActorType, "other"},
		ActorAddress: &addr,
		PullSlots: []*schedulerv1pb.PullSlots{
			{ActorType: pullActorType, Free: 2},
			{ActorType: "nothosted", Free: 3},
		},
	})

	require.Len(t, c.pullPools[pullActorType], 1)
	pool := c.pullPools[pullActorType][0]
	assert.Equal(t, addr, pool.sidecar)
	assert.Equal(t, uint32(2), pool.credit)
	assert.Equal(t, []uint64{7}, pool.streams)
	assert.NotContains(t, c.pullPools, "nothosted", "slots for a type the stream does not host are ignored")
	assert.Equal(t, addr, c.streamSidecars[7])

	c.addPullStream(8, &schedulerv1pb.WatchJobsRequestInitial{ActorTypes: []string{pullActorType}})
	assert.Equal(t, "idx:8", c.streamSidecars[8], "a stream without an actor address is its own sidecar")
	assert.Len(t, c.pullPools[pullActorType], 1, "no slots reported, no pool")
}

// TestPullDispatch_ThrottledKeyIsSharedGateOnly pins that tryDispatchPull
// names the shared gate that blocked a trigger, and nothing when the only
// obstacle was a full sidecar: waiting for a slot is the backlog gauge's job,
// not a concurrency throttle.
func TestPullDispatch_ThrottledKeyIsSharedGateOnly(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 2)
	h.c.concurrencyGates[pullActorType] = &concurrencyGate{globalLimit: 1}
	h.c.streamGateKeys[0] = append(h.c.streamGateKeys[0], pullActorType)

	dispatched, key := h.c.tryDispatchPull(h.trigger("wf-1::0::0", "act"))
	assert.True(t, dispatched)
	assert.Empty(t, key)

	dispatched, key = h.c.tryDispatchPull(h.trigger("wf-2::0::0", "act"))
	assert.False(t, dispatched)
	assert.Equal(t, pullActorType, key, "the type gate is full while a slot is free")

	// Room on the type gate again, but the sidecar has no slot left.
	h.c.concurrencyGates[pullActorType].globalLimit = 10
	h.report(0, 0)
	dispatched, key = h.c.tryDispatchPull(h.trigger("wf-3::0::0", "act"))
	assert.False(t, dispatched)
	assert.Empty(t, key, "no free slot is not a concurrency throttle")
}

func TestPullDispatch_NamespaceKeptWhileFlightsHeld(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)
	var closes int
	h.c.nsLoop = loopfake.New[loops.EventNS]().WithEnqueue(func(e loops.EventNS) {
		if _, ok := e.(*loops.ConnCloseNamespace); ok {
			closes++
		}
	})

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	h.closeStream(t, 0, running)
	assert.Equal(t, 0, closes, "the loop holding an orphaned pull trigger stays for its sidecar to reattach")

	running.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
	assert.Equal(t, 1, closes, "the loop closes once the last held trigger resolves")
}

func TestPullDispatch_AdoptsReminderListedAsRunning(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 0, 0)

	// This scheduler restarted: the sidecar lists a reminder it is running
	// that this scheduler has no record of, and reports no free slot.
	h.dispatched = append(h.dispatched, nil, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: 0}},
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})
	h.addStream(t, 1, 1)
	h.report(1, 0)

	// The cron fires it again: it goes back to the sidecar running it,
	// without a slot, instead of waiting in the backlog.
	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	require.Len(t, h.dispatched[0], 1)
	assert.Same(t, running, h.dispatched[0][0])
	assert.Equal(t, uint32(0), h.pool(0).credit)
	assert.Empty(t, h.c.pullPending)

	// A claim is used once.
	again := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(again)
	assert.Len(t, h.dispatched[0], 1)
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len())
}

func TestPullDispatch_ClaimWaitsForItsSidecar(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 0, 0)
	h.dispatched = append(h.dispatched, nil, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: 0}},
	})
	h.addStream(t, 1, 1)
	h.c.concurrencyGates[pullActorType+":act"] = &concurrencyGate{globalLimit: 1, current: 1}
	h.c.streamGateKeys[0] = append(h.c.streamGateKeys[0], pullActorType+":act")

	// The per-name gate is full: the claimed reminder waits for it rather
	// than going to the sidecar with a free slot.
	req := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(req)
	assert.Empty(t, h.dispatched[1])
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len())
	require.Contains(t, h.c.pullClaims, reminderKey(pullActorType, "wf::1::0", "run-activity"), "a claim is kept until it dispatches")

	h.c.concurrencyGates[pullActorType+":act"].current = 0
	h.c.drainPullPending()
	require.Len(t, h.dispatched[0], 1, "it goes to the sidecar that claimed it")
	assert.Empty(t, h.dispatched[1])
	assert.Empty(t, h.c.pullClaims)
}

func TestPullDispatch_ClaimKeepsAnEarlyResult(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 0, 0)
	h.dispatched = append(h.dispatched, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: 1}},
	})

	// The activity finishes before the cron fires it again.
	h.c.handlePullResult(&loops.PullResult{Sidecar: addr(0), Reminder: ref("wf::1::0"), Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS})
	// A later listing from the sidecar no longer includes it.
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots: []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: 1}},
	})

	req := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(req)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.results[req], "resolved with the result the sidecar sent")
	assert.Empty(t, h.dispatched[0], "a finished activity is not delivered again")
	assert.Empty(t, h.c.pullClaims)
}

func TestPullDispatch_HeldGatesSurviveTheStream(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)
	limit := pullActorType + ":act"
	h.c.updateConcurrencyLimits(0, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{{
			MaxConcurrent: 1,
			Name:          new("act"),
			Target:        &schedulerv1pb.ConcurrencyLimit_Actor{Actor: &schedulerv1pb.ConcurrencyLimitActor{Type: pullActorType}},
		}},
	})

	running := h.trigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	require.Equal(t, uint32(1), h.c.concurrencyGates[limit].current)

	h.closeStream(t, 0, running)
	gate, ok := h.c.concurrencyGates[limit]
	require.True(t, ok, "a gate held by a running activity outlives the stream that delivered it")
	assert.Equal(t, uint32(1), gate.current)
}
