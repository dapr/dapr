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

package pull

import (
	"strconv"
	"testing"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func TestDispatcher_FillsFreeSlotsAcrossSidecars(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3, 1)

	for i := range 3 {
		h.handle(h.trigger("wf-a::"+strconv.Itoa(i)+"::0", "act"))
	}

	for i := range 3 {
		assert.Len(t, h.dispatched[i], 1, "each single-slot sidecar takes exactly one trigger")
		assert.Equal(t, uint32(0), h.pool(i).credit)
	}
}

func TestDispatcher_PrefersMostFreeSlots(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2, 4)
	h.report(0, 1)

	for i := range 3 {
		h.handle(h.trigger("wf::"+strconv.Itoa(i)+"::0", "act"))
	}
	assert.Empty(t, h.dispatched[0])
	assert.Len(t, h.dispatched[1], 3)

	// Now both have one free slot: the next two go one each.
	h.handle(h.trigger("wf::4::0", "act"))
	h.handle(h.trigger("wf::5::0", "act"))
	assert.Len(t, h.dispatched[0], 1)
	assert.Len(t, h.dispatched[1], 4)
}

func TestDispatcher_CreditComesFromReportsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2, 1)

	first := h.trigger("wf::1::0", "act")
	second := h.trigger("wf::2::0", "act")
	third := h.trigger("wf::3::0", "act")
	h.handle(first)
	h.handle(second)
	h.handle(third)

	assert.Equal(t, 2, h.totalDispatched())
	require.Contains(t, h.d.pending, testActorType)
	assert.Equal(t, 1, h.d.pending[testActorType].len())
	assert.NotContains(t, h.results, third, "a parked trigger has no result yet")

	h.ack(t, first)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.resultOf(t, first))
	assert.Equal(t, 2, h.totalDispatched(), "a completion alone frees nothing: the sidecar may still be full")

	h.report(1, 0)
	assert.Equal(t, 2, h.totalDispatched())

	h.report(0, 1)
	assert.Equal(t, 3, h.totalDispatched(), "the reported slot takes the parked trigger")
	assert.Same(t, third, h.dispatched[0][1])
	assert.NotContains(t, h.d.pending, testActorType, "the emptied queue is dropped")
}

func TestDispatcher_SharedGateFullParks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2, 5)

	// A global per-name gate of 1 for "act" shared by both sidecars.
	h.gates[testActorType+":act"] = &gate{limit: 1}

	first := h.trigger("wf::1::0", "act")
	second := h.trigger("wf::2::0", "act")
	other := h.trigger("wf::3::0", "other")
	h.handle(first)
	h.handle(second)
	h.handle(other)

	assert.Equal(t, 2, h.totalDispatched(), "the second 'act' waits on the per-name gate; 'other' is unaffected")
	assert.Equal(t, 1, h.d.pending[testActorType].len())
	assert.Equal(t, uint32(8), h.pool(0).credit+h.pool(1).credit, "a failed shared-gate acquire takes no slot")

	h.ack(t, first)
	assert.Equal(t, 3, h.totalDispatched())
	assert.Equal(t, uint32(1), h.gates[testActorType+":act"].current)
}

func TestDispatcher_RoundRobinsAcrossInstances(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	running := h.trigger("busy::0::0", "act")
	h.handle(running)
	require.Equal(t, 1, h.totalDispatched())

	// Instance A fans out five tasks, then instance B schedules one.
	a := make([]*loops.TriggerRequest, 0, 5)
	for i := range 5 {
		req := h.trigger("wf-a::"+strconv.Itoa(i)+"::0", "act")
		a = append(a, req)
		h.handle(req)
	}
	b := h.trigger("wf-b::1::0", "act")
	h.handle(b)
	require.Equal(t, 6, h.d.pending[testActorType].len())

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

func TestDispatcher_FallsBackWithoutSlots(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)

	req := h.trigger("wf::1::0", "act")
	h.handle(req)

	assert.Equal(t, []*loops.TriggerRequest{req}, h.routed, "with no sidecar reporting slots the trigger takes the regular route")
	assert.Nil(t, req.Repark, "a regularly routed trigger is not tracked as a pull delivery")
	assert.False(t, req.Job.GetMetadata().GetPull(), "the receiving sidecar must route it like any reminder")
	assert.Empty(t, h.d.pending)
}

func TestDispatcher_NonPullTriggersIgnoreSlots(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	plain := h.trigger("wf::1::0", "act")
	plain.Job.Metadata.Pull = nil
	h.handle(plain)
	h.handle(h.trigger("wf::2::0", "act"))

	assert.Equal(t, []*loops.TriggerRequest{plain}, h.routed)
	assert.Len(t, h.dispatched[0], 1)
	assert.Equal(t, uint32(0), h.pool(0).credit, "only the pull trigger took the slot")
}

// TestDispatcher_ThrottledKeyIsSharedGateOnly pins that tryDispatch names the
// shared gate that blocked a trigger, and nothing when the only obstacle was
// a full sidecar: waiting for a slot is the backlog gauge's job, not a
// concurrency throttle.
func TestDispatcher_ThrottledKeyIsSharedGateOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 2)
	h.gates[testActorType] = &gate{limit: 1}

	dispatched, key := h.d.tryDispatch(h.trigger("wf-1::0::0", "act"))
	assert.True(t, dispatched)
	assert.Empty(t, key)

	dispatched, key = h.d.tryDispatch(h.trigger("wf-2::0::0", "act"))
	assert.False(t, dispatched)
	assert.Equal(t, testActorType, key, "the type gate is full while a slot is free")

	// Room on the type gate again, but the sidecar has no slot left.
	h.gates[testActorType].limit = 10
	h.report(0, 0)
	dispatched, key = h.d.tryDispatch(h.trigger("wf-3::0::0", "act"))
	assert.False(t, dispatched)
	assert.Empty(t, key, "no free slot is not a concurrency throttle")
}

func TestDispatcher_FullNameGateDoesNotHoldBackOtherNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 0)
	h.gates[testActorType+":slow"] = &gate{limit: 1, current: 1}

	// One instance parks a Slow activity, whose gate is full, then a Fast one.
	slow := h.trigger("wf::1::0", "slow")
	fast := h.trigger("wf::2::0", "fast")
	h.handle(slow)
	h.handle(fast)
	require.Equal(t, 2, h.d.pending[testActorType].len())

	h.report(0, 1)
	require.Len(t, h.dispatched[0], 1)
	assert.Same(t, fast, h.dispatched[0][0], "Fast is not held back by Slow's gate")
}

func TestDispatcher_DrainStopsWhenSlotsRunOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 0)
	for i := range 5 {
		h.handle(h.trigger("wf-"+strconv.Itoa(i)+"::1::0", "act"))
	}
	require.Equal(t, 5, h.d.pending[testActorType].len())

	before := h.d.rotate
	h.report(0, 1)
	require.Len(t, h.dispatched[0], 1)
	assert.Equal(t, 1, h.d.rotate-before, "with the only slot taken, no other parked instance is tried")
}

func TestDispatcher_DrainStopsWhenTypeGateFull(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 10)
	h.gates[testActorType] = &gate{limit: 1}

	running := h.trigger("wf-0::1::0", "act")
	h.handle(running)
	for i := 1; i <= 5; i++ {
		h.handle(h.trigger("wf-"+strconv.Itoa(i)+"::1::0", "act"))
	}
	require.Len(t, h.dispatched[0], 1)
	require.Equal(t, 5, h.d.pending[testActorType].len())

	h.acquires = 0
	h.d.Drain()
	assert.Equal(t, 1, h.acquires, "with the type gate full, the first refusal ends the rotation")
	assert.Len(t, h.dispatched[0], 1)

	h.ack(t, running)
	assert.Len(t, h.dispatched[0], 2, "the released gate admits one parked instance")
	assert.Equal(t, 4, h.d.pending[testActorType].len())
}
