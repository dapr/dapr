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
	"strconv"
	"testing"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/kit/events/loop"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

const pullActorType = "dapr.internal.default.myapp.activity"

// pullHarness is a connections loop with n pull-eligible streams of `slots`
// slots each, recording what each stream was handed.
type pullHarness struct {
	c          *connections
	dispatched [][]*loops.TriggerRequest
	results    map[*loops.TriggerRequest]api.TriggerResponseResult
}

func newPullHarness(t *testing.T, streams int, slots int32) *pullHarness {
	t.Helper()

	h := &pullHarness{
		c:          connsCache.New().(*connections),
		dispatched: make([][]*loops.TriggerRequest, streams),
		results:    make(map[*loops.TriggerRequest]api.TriggerResponseResult),
	}
	h.c.schedulerCount = 1
	h.c.nsLoop = loopfake.New[loops.EventNS]()
	h.c.loop = loopfake.New[loops.EventConn]().WithEnqueue(func(e loops.EventConn) {
		// Releases are enqueued by the wrapped ResultFn; apply them inline so
		// tests observe the drain synchronously.
		if rel, ok := e.(*loops.ConcurrencyRelease); ok {
			h.c.handleConcurrencyRelease(rel)
		}
	})

	for i := range streams {
		h.addStream(t, i, slots)
	}
	return h
}

// addr is the actor address of the sidecar behind stream i; each index is
// its own sidecar unless a test connects a second stream with the same
// address.
func addr(i int) string {
	return "127.0.0.1:" + strconv.Itoa(1000+i)
}

// key is the slot gate key of the sidecar behind stream i.
func (h *pullHarness) key(i int) string {
	return pullGateKey(addr(i), pullActorType)
}

// addStream registers a stream at index i for its own sidecar with a slot
// limit, mirroring what handleAdd does after the stream loop is created.
func (h *pullHarness) addStream(t *testing.T, i int, slots int32) uint64 {
	t.Helper()
	return h.addStreamFor(t, i, addr(i), slots)
}

// addStreamFor registers a stream recording into dispatched[i] on behalf of
// the sidecar at actorAddress.
func (h *pullHarness) addStreamFor(t *testing.T, i int, actorAddress string, slots int32) uint64 {
	t.Helper()
	idx := h.c.streamIDx
	h.c.streamIDx++

	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		h.dispatched[i] = append(h.dispatched[i], e.(*loops.TriggerRequest))
	})
	h.c.streams[idx] = h.c.streamPool.Add(store.Options{
		Loop:         streamLoop,
		ActorTypes:   []string{pullActorType},
		ActorAddress: &actorAddress,
	})
	h.c.streamLoops[idx] = streamLoop
	h.c.updateConcurrencyLimits(idx, &schedulerv1pb.WatchJobsRequestInitial{
		ActorTypes:   []string{pullActorType},
		ActorAddress: &actorAddress,
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{{
			MaxConcurrent: slots,
			Target: &schedulerv1pb.ConcurrencyLimit_Stream{Stream: &schedulerv1pb.ConcurrencyLimitStream{
				ActorType: pullActorType,
			}},
		}},
	})
	h.c.drainPullPending()
	return idx
}

func (h *pullHarness) trigger(actorID, name string) *loops.TriggerRequest {
	req := makeTriggerRequest(pullActorType, actorID, name)
	req.Job.Metadata.Pull = new(true)
	req.Job.Metadata.Namespace = "default"
	req.Job.Metadata.AppId = "myapp"
	req.ResultFn = func(r api.TriggerResponseResult) { h.results[req] = r }
	return req
}

// ack completes a dispatched trigger the way the stream loop does when the
// sidecar reports the result, releasing its gates.
func (h *pullHarness) ack(req *loops.TriggerRequest) {
	req.ResultFn(api.TriggerResponseResult_SUCCESS)
}

func (h *pullHarness) totalDispatched() int {
	n := 0
	for _, d := range h.dispatched {
		n += len(d)
	}
	return n
}

func TestPullDispatch_FillsFreeSlotsAcrossStreams(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 3, 1)

	for i := range 3 {
		h.c.handleTriggerRequest(h.trigger("wf-a::"+string(rune('1'+i))+"::0", "act"))
	}

	for i := range 3 {
		assert.Len(t, h.dispatched[i], 1, "each single-slot stream takes exactly one trigger")
	}
	for i := range 3 {
		gate := h.c.concurrencyGates[h.key(i)]
		assert.Equal(t, uint32(1), gate.current)
	}
}

func TestPullDispatch_PrefersMostFreeSlots(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 4)

	// Pre-load stream 0 so stream 1 has more free slots.
	h.c.concurrencyGates[h.key(0)].current = 3

	h.c.handleTriggerRequest(h.trigger("wf::1::0", "act"))
	h.c.handleTriggerRequest(h.trigger("wf::2::0", "act"))
	h.c.handleTriggerRequest(h.trigger("wf::3::0", "act"))

	assert.Empty(t, h.dispatched[0])
	assert.Len(t, h.dispatched[1], 3)

	// Now both have one free slot: the next two go one each.
	h.c.handleTriggerRequest(h.trigger("wf::4::0", "act"))
	h.c.handleTriggerRequest(h.trigger("wf::5::0", "act"))
	assert.Len(t, h.dispatched[0], 1)
	assert.Len(t, h.dispatched[1], 4)
}

func TestPullDispatch_ParksWhenFullAndDrainsOnAck(t *testing.T) {
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

	h.ack(first)

	assert.Equal(t, 3, h.totalDispatched(), "the ack frees a slot and the parked trigger is dispatched")
	assert.NotContains(t, h.c.pullPending, pullActorType, "the emptied queue is dropped")
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.results[first])
}

func TestPullDispatch_SharedGateFullParks(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 2, 5)

	// A global per-name gate of 1 for "act" shared by both streams.
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
	for i := range 2 {
		gate := h.c.concurrencyGates[h.key(i)]
		assert.LessOrEqual(t, gate.current, uint32(1), "a failed shared-gate acquire must release the stream slot")
	}

	h.ack(first)
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
		req := h.trigger("wf-a::"+string(rune('1'+i))+"::0", "act")
		a = append(a, req)
		h.c.handleTriggerRequest(req)
	}
	b := h.trigger("wf-b::1::0", "act")
	h.c.handleTriggerRequest(b)
	require.Equal(t, 6, h.c.pullPending[pullActorType].len())

	// Each ack frees the single slot; the drain must alternate A, B rather
	// than serving all of A first.
	h.ack(running)
	require.Len(t, h.dispatched[0], 2)
	assert.Same(t, a[0], h.dispatched[0][1])

	h.ack(a[0])
	require.Len(t, h.dispatched[0], 3)
	assert.Same(t, b, h.dispatched[0][2], "instance B is served before A's second task")

	h.ack(b)
	require.Len(t, h.dispatched[0], 4)
	assert.Same(t, a[1], h.dispatched[0][3])
}

func TestPullDispatch_FallsBackWithoutSlotStreams(t *testing.T) {
	t.Parallel()

	var dispatched []loops.EventStream
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		dispatched = append(dispatched, e)
	})
	pool := store.New()
	pool.Add(store.Options{Loop: streamLoop, ActorTypes: []string{pullActorType}})

	c := &connections{
		streamPool:       pool,
		concurrencyGates: make(map[string]*concurrencyGate),
		streamGateKeys:   make(map[uint64][]string),
		pullPools:        make(map[string][]*pullPool),
		pullPending:      make(map[string]*pullQueue),
		schedulerCount:   1,
	}

	req := makeTriggerRequest(pullActorType, "wf::1::0", "act")
	req.Job.Metadata.Pull = new(true)
	c.handleTriggerRequest(req)

	require.Len(t, dispatched, 1, "a pull trigger with no slot-declaring stream uses the regular actor routing")
	assert.Same(t, req, dispatched[0].(*loops.TriggerRequest))
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
	gate := h.c.concurrencyGates[h.key(0)]
	assert.Equal(t, uint32(1), gate.current, "only the pull trigger consumed a stream slot")
}

func TestPullDispatch_LastStreamClosingReturnsPending(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)
	h.c.streams[0] = func() {}

	running := h.trigger("wf::1::0", "act")
	parked := h.trigger("wf::2::0", "act")
	h.c.handleTriggerRequest(running)
	h.c.handleTriggerRequest(parked)
	require.Equal(t, 1, h.c.pullPending[pullActorType].len())

	h.c.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})

	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results[parked], "parked triggers go back to the cron for retry")
	assert.Empty(t, h.c.pullPending)
	assert.Empty(t, h.c.pullPools)
	assert.NotContains(t, h.c.concurrencyGates, h.key(0))
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

	require.Len(t, h.dispatched[1], 1, "a newly connected stream with slots takes the parked trigger")
	assert.Same(t, parked, h.dispatched[1][0])
}

func TestPullDispatch_ShutdownReturnsPending(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)
	h.c.streams[0] = func() {}

	h.c.handleTriggerRequest(h.trigger("wf::1::0", "act"))
	parked := h.trigger("wf::2::0", "act")
	h.c.handleTriggerRequest(parked)

	h.c.handleShutdown()
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results[parked])
}

func TestPullDispatch_SameSidecarStreamsShareSlots(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t, 1, 1)
	h.c.streams[0] = func() {}

	// The same sidecar reconnects (actor types changed) and, for a while,
	// holds two streams. They must share the sidecar's single slot.
	h.dispatched = append(h.dispatched, nil)
	second := h.addStreamFor(t, 1, addr(0), 1)
	h.c.streams[second] = func() {}
	require.Len(t, h.c.pullPools[pullActorType], 1, "one pool per sidecar, not per stream")
	assert.Equal(t, []uint64{0, second}, h.c.pullPools[pullActorType][0].streams)

	first := h.trigger("wf::1::0", "act")
	parked := h.trigger("wf::2::0", "act")
	h.c.handleTriggerRequest(first)
	h.c.handleTriggerRequest(parked)

	assert.Empty(t, h.dispatched[0], "delivery prefers the sidecar's newest stream")
	assert.Len(t, h.dispatched[1], 1)
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len(), "the second trigger waits for the sidecar's only slot")

	// The old stream closing does not remove the sidecar or its slot.
	h.c.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})
	require.Len(t, h.c.pullPools[pullActorType], 1)
	assert.Equal(t, []uint64{second}, h.c.pullPools[pullActorType][0].streams)
	assert.Equal(t, 1, h.c.pullPending[pullActorType].len())

	h.ack(first)
	assert.Len(t, h.dispatched[1], 2)
	assert.Same(t, parked, h.dispatched[1][1])
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

func TestUpdateConcurrencyLimits_StreamGateIsPerStream(t *testing.T) {
	t.Parallel()
	c := connsCache.New().(*connections)
	c.schedulerCount = 3
	c.schedulerIdx = 2
	c.streamLoops[7] = loop.New[loops.EventStream](1).NewLoop(nil)

	c.updateConcurrencyLimits(7, &schedulerv1pb.WatchJobsRequestInitial{
		ActorTypes: []string{pullActorType, "ignored"},
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{
			{MaxConcurrent: 2, Target: &schedulerv1pb.ConcurrencyLimit_Stream{Stream: &schedulerv1pb.ConcurrencyLimitStream{ActorType: pullActorType}}},
			{MaxConcurrent: 0, Target: &schedulerv1pb.ConcurrencyLimit_Stream{Stream: &schedulerv1pb.ConcurrencyLimitStream{ActorType: "ignored"}}},
			{MaxConcurrent: 3, Target: &schedulerv1pb.ConcurrencyLimit_Stream{Stream: &schedulerv1pb.ConcurrencyLimitStream{ActorType: "nothosted"}}},
			{MaxConcurrent: 9, Target: &schedulerv1pb.ConcurrencyLimit_Actor{Actor: &schedulerv1pb.ConcurrencyLimitActor{Type: pullActorType}}},
		},
	})

	gate := c.concurrencyGates[streamGateKey(7, pullActorType)]
	require.NotNil(t, gate)
	assert.True(t, gate.perStream)
	assert.Equal(t, uint32(2), gate.free(3, 2), "per-stream limits are not divided across schedulers")
	assert.Equal(t, uint32(3), c.concurrencyGates[pullActorType].free(3, 2), "global limits still are")
	require.Len(t, c.pullPools[pullActorType], 1)
	assert.Equal(t, streamGateKey(7, pullActorType), c.pullPools[pullActorType][0].key)
	assert.Equal(t, []uint64{7}, c.pullPools[pullActorType][0].streams)
	assert.NotContains(t, c.pullPools, "ignored")
	assert.NotContains(t, c.pullPools, "nothosted", "slots for a type the stream does not host are ignored")
	assert.ElementsMatch(t, []string{streamGateKey(7, pullActorType), pullActorType}, c.streamGateKeys[7])
}
