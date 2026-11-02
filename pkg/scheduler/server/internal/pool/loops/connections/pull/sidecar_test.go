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
	"testing"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func TestDispatcher_AddStreamEligibility(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)
	addr := "10.0.0.1:50001"

	h.d.AddStream(7, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:        testAppID,
		ActorTypes:   []string{testActorType, "other"},
		ActorAddress: &addr,
		PullSlots: []*schedulerv1pb.PullSlots{
			{ActorType: testActorType, Free: 2},
			{ActorType: "nothosted", Free: 3},
		},
	})

	require.Len(t, h.d.pools[testActorType], 1)
	p := h.d.pools[testActorType][0]
	assert.Equal(t, "myapp/"+addr, p.sidecar)
	assert.Equal(t, uint32(2), p.credit)
	assert.Equal(t, []uint64{7}, p.streams)
	assert.NotContains(t, h.d.pools, "nothosted", "slots for a type the stream does not host are ignored")
	assert.Equal(t, "myapp/"+addr, h.d.streams[7])

	h.d.AddStream(9, &schedulerv1pb.WatchJobsRequestInitial{AppId: "otherapp", ActorAddress: &addr})
	assert.NotEqual(t, h.d.streams[7], h.d.streams[9], "another app reporting the same address is another sidecar")

	h.d.AddStream(8, &schedulerv1pb.WatchJobsRequestInitial{ActorTypes: []string{testActorType}})
	assert.Equal(t, "idx:8", h.d.streams[8], "a stream without an actor address is its own sidecar")
	assert.Len(t, h.d.pools[testActorType], 1, "no slots reported, no pool")
}

func TestDispatcher_SameSidecarStreamsShareSlots(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	// The same sidecar reconnects (actor types changed) and, for a while,
	// holds two streams. They share the sidecar's slots.
	h.dispatched = append(h.dispatched, nil)
	second := h.addStreamFor(t, 1, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots: []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 1}},
	})
	require.Len(t, h.d.pools[testActorType], 1, "one pool per sidecar, not per stream")
	assert.Equal(t, []uint64{0, second}, h.pool(0).streams)
	assert.Equal(t, 2, h.d.sidecars[sidecarOf(0)].streams)

	first := h.trigger("wf::1::0", "act")
	parked := h.trigger("wf::2::0", "act")
	h.handle(first)
	h.handle(parked)

	assert.Empty(t, h.dispatched[0], "delivery prefers the sidecar's newest stream")
	assert.Len(t, h.dispatched[1], 1)
	assert.Equal(t, 1, h.d.pending[testActorType].len(), "the second trigger waits for the sidecar's only slot")

	// The old stream closing does not remove the sidecar or its slots.
	h.closeStream(t, 0)
	require.Len(t, h.d.pools[testActorType], 1)
	assert.Equal(t, []uint64{second}, h.pool(0).streams)
	assert.Equal(t, 1, h.d.pending[testActorType].len())

	h.ack(t, first)
	h.report(0, 1)
	assert.Len(t, h.dispatched[1], 2)
	assert.Same(t, parked, h.dispatched[1][1])
}

func TestDispatcher_NewStreamDrainsPending(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	h.handle(h.trigger("wf::1::0", "act"))
	parked := h.trigger("wf::2::0", "act")
	h.handle(parked)
	require.Equal(t, 1, h.d.pending[testActorType].len())

	h.dispatched = append(h.dispatched, nil)
	h.addStream(t, 1, 1)

	require.Len(t, h.dispatched[1], 1, "a newly connected sidecar with slots takes the parked trigger")
	assert.Same(t, parked, h.dispatched[1][0])
}

func TestDispatcher_LastStreamClosingReturnsPending(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	parked := h.trigger("wf::2::0", "act")
	h.handle(running)
	h.handle(parked)
	require.Equal(t, 1, h.d.pending[testActorType].len())

	h.closeStream(t, 0, running)

	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.resultOf(t, parked), "parked triggers go back to the cron for retry")
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.resultOf(t, running), "the running trigger goes back with its stream")
	assert.Empty(t, h.d.pending)
	assert.Empty(t, h.d.pools)
	assert.Empty(t, h.d.sidecars, "a sidecar with no streams and no flights is forgotten")
}

func TestDispatcher_ReportsApplyPerSidecar(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 2)
	h.handle(h.trigger("wf::1::0", "act"))
	require.Equal(t, uint32(1), h.pool(0).credit)

	h.d.HandleSlots(&loops.PullSlotsUpdate{AppID: testAppID, ActorAddress: "unknown", ActorType: testActorType, Free: 9})
	h.d.HandleSlots(&loops.PullSlotsUpdate{AppID: testAppID, ActorAddress: addr(0), ActorType: "other", Free: 9})
	assert.Equal(t, uint32(1), h.pool(0).credit)
	h.report(0, 3)
	assert.Equal(t, uint32(3), h.pool(0).credit)
}
