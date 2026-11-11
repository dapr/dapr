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
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func TestDispatcher_NoCapacityReparks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2, 1)
	h.gates[testActorType] = &gate{limit: 10}
	h.report(1, 0)

	req := h.trigger("wf::1::0", "act")
	h.handle(req)
	require.Len(t, h.dispatched[0], 1)
	require.Equal(t, uint32(1), h.gates[testActorType].current)

	// Another scheduler filled the sidecar's last slot first.
	req.Repark()
	h.pump(t)

	assert.NotContains(t, h.results, req, "a refused trigger is not resolved to the cron")
	assert.Equal(t, uint32(0), h.pool(0).credit, "the refusing sidecar is full until it reports otherwise")
	assert.Equal(t, uint32(0), h.gates[testActorType].current, "the shared gate is released")
	assert.Equal(t, 1, h.d.pending[testActorType].len())
	assert.Empty(t, h.d.sidecars[sidecarOf(0)].flights)

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
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.resultOf(t, req), "the cron callback travels with the requeued trigger")
}

func TestDispatcher_ReattachAfterReconnect(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2, 1)
	h.report(1, 0)

	running := h.trigger("wf::1::0", "act")
	h.handle(running)
	require.Len(t, h.dispatched[0], 1)

	h.closeStream(t, 0, running)
	f := h.d.sidecars[sidecarOf(0)].flights[key("wf::1::0")]
	require.NotNil(t, f)
	require.NotNil(t, f.timer, "an orphaned flight waits a bounded time for its sidecar")

	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 0}},
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})
	assert.True(t, f.confirmed)
	assert.Nil(t, f.timer, "a sidecar that lists the reminder keeps it without a deadline")
	h.report(1, 1)
	assert.Empty(t, h.dispatched[1], "the reattached trigger is not delivered elsewhere")

	h.result(0, ref("wf::1::0"), schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	h.pump(t)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.resultOf(t, running))
	assert.NotContains(t, h.d.sidecars[sidecarOf(0)].flights, key("wf::1::0"))
}

func TestDispatcher_ConfirmedBeforeOrphaned(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	h.handle(running)

	// The sidecar's new stream arrives before this scheduler notices the old
	// one closing.
	h.dispatched = append(h.dispatched, nil)
	h.addStreamFor(t, 1, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})
	h.closeStream(t, 0, running)

	f := h.d.sidecars[sidecarOf(0)].flights[key("wf::1::0")]
	require.NotNil(t, f)
	assert.True(t, f.orphaned)
	assert.Nil(t, f.timer, "a confirmed flight on a live sidecar has no deadline")

	// The sidecar's last stream closes: the deadline starts again.
	h.closeStream(t, 1)
	assert.False(t, f.confirmed)
	assert.NotNil(t, f.timer)
}

func TestDispatcher_UnlistedOrphanStartsTimer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	h.handle(running)
	h.closeStream(t, 0, running)
	f := h.d.sidecars[sidecarOf(0)].flights[key("wf::1::0")]

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

func TestDispatcher_OrphanExpires(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2, 1)
	h.d.grace = time.Millisecond
	h.report(1, 0)

	running := h.trigger("wf::1::0", "act")
	h.handle(running)
	require.Len(t, h.dispatched[0], 1)
	h.closeStream(t, 0, running)

	// The timer may already have fired while closeStream pumped.
	timeout := time.After(5 * time.Second)
	for {
		if _, ok := h.results[running]; ok {
			break
		}
		select {
		case e := <-h.events:
			e.(*loops.ConnRun).Fn()
		case <-timeout:
			require.FailNow(t, "the reattach timer did not fire")
		}
	}
	h.pump(t)

	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.resultOf(t, running), "an unclaimed orphan goes back to the cron")
	assert.NotContains(t, h.d.sidecars, sidecarOf(0), "a sidecar with no streams and no flights is forgotten")
	assert.Equal(t, 1, h.idles, "the last resolved flight leaves the Dispatcher idle")
}

func TestDispatcher_AdoptsReminderListedAsRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)

	// This scheduler restarted: the sidecar lists a reminder it is running
	// that this scheduler has no record of, and reports no free slot.
	h.dispatched = append(h.dispatched, nil, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 0}},
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})
	h.addStream(t, 1, 1)
	h.report(1, 0)

	// The cron fires it again: it goes back to the sidecar running it,
	// without a slot, instead of waiting in the backlog.
	running := h.trigger("wf::1::0", "act")
	h.handle(running)
	require.Len(t, h.dispatched[0], 1)
	assert.Same(t, running, h.dispatched[0][0])
	assert.Equal(t, uint32(0), h.pool(0).credit)
	assert.Empty(t, h.d.pending)

	// A claim is used once.
	again := h.trigger("wf::1::0", "act")
	h.handle(again)
	assert.Len(t, h.dispatched[0], 1)
	assert.Equal(t, 1, h.d.pending[testActorType].len())
}

func TestDispatcher_ClaimWaitsForItsSidecar(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)
	h.dispatched = append(h.dispatched, nil, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 0}},
	})
	h.addStream(t, 1, 1)
	h.gates[testActorType+":act"] = &gate{limit: 1, current: 1}

	// The per-name gate is full: the claimed reminder waits for it rather
	// than going to the sidecar with a free slot.
	req := h.trigger("wf::1::0", "act")
	h.handle(req)
	assert.Empty(t, h.dispatched[1])
	assert.Equal(t, 1, h.d.pending[testActorType].len())
	require.Contains(t, h.d.claims, key("wf::1::0"), "a claim is kept until it dispatches")

	h.gates[testActorType+":act"].current = 0
	h.d.Drain()
	require.Len(t, h.dispatched[0], 1, "it goes to the sidecar that claimed it")
	assert.Empty(t, h.dispatched[1])
	assert.Empty(t, h.d.claims)
}

func TestDispatcher_ClaimKeepsAnEarlyResult(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)
	h.dispatched = append(h.dispatched, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 1}},
	})

	// The activity finishes before the cron fires it again.
	h.result(0, ref("wf::1::0"), schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	// A later listing from the sidecar no longer includes it.
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots: []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 1}},
	})

	req := h.trigger("wf::1::0", "act")
	h.handle(req)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.resultOf(t, req), "resolved with the result the sidecar sent")
	assert.Empty(t, h.dispatched[0], "a finished activity is not delivered again")
	assert.Empty(t, h.d.claims)
}

func TestDispatcher_ParkedClaimResolvesWithItsResult(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)
	h.dispatched = append(h.dispatched, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 0}},
	})
	h.gates[testActorType+":act"] = &gate{limit: 1, current: 1}

	// The cron fires the claimed reminder while its per-name gate is full.
	req := h.trigger("wf::1::0", "act")
	h.handle(req)
	require.Equal(t, 1, h.d.pending[testActorType].len())

	// The sidecar's result arrives while the trigger is parked.
	h.result(0, ref("wf::1::0"), schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.resultOf(t, req), "resolved with the result the sidecar sent")
	assert.Empty(t, h.d.pending)
	assert.Empty(t, h.dispatched[0], "a finished activity is not delivered again")
	assert.Empty(t, h.d.claims)
}

func TestDispatcher_ClaimResultOutlivesItsSidecar(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)
	h.dispatched = append(h.dispatched, nil, nil)
	idx := h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
		PullSlots:         []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: 0}},
	})
	h.addStream(t, 1, 1)

	// The activity finishes and the sidecar leaves before the cron fires the
	// reminder again.
	h.result(0, ref("wf::1::0"), schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	h.closeStream(t, idx)

	req := h.trigger("wf::1::0", "act")
	h.handle(req)
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, h.resultOf(t, req), "resolved with the result the sidecar sent")
	assert.Empty(t, h.dispatched[1], "a finished activity is not delivered to another sidecar")
}

func TestDispatcher_ClaimedReminderWithoutPoolsIsNotRoutedElsewhere(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)

	// The sidecar running it reconnects before it reports slots for the type.
	h.dispatched = append(h.dispatched, nil)
	h.addStreamFor(t, 0, addr(0), &schedulerv1pb.WatchJobsRequestInitial{
		InflightReminders: []*schedulerv1pb.ActorReminderRef{ref("wf::1::0")},
	})

	req := h.trigger("wf::1::0", "act")
	h.handle(req)
	assert.Empty(t, h.routed, "regular routing would run it a second time")
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.resultOf(t, req), "the cron retries once a sidecar takes pull work")
}

func TestDispatcher_NoCapacityAtTheBoundStillParks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)
	h.d.opts.MaxParked = 1

	running := h.trigger("wf-a::1::0", "act")
	h.handle(running)
	parked := h.trigger("wf-b::1::0", "act")
	h.handle(parked)
	require.Len(t, h.dispatched[0], 1)
	require.Equal(t, 1, h.d.pending[testActorType].len(), "the queue is at its bound")

	// The sidecar refuses the dispatched trigger.
	running.Repark()
	h.pump(t)

	assert.NotContains(t, h.results, running, "a refused trigger never reaches its failure policy, even with the queue full")
	assert.Equal(t, 2, h.d.pending[testActorType].len())
}
