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
	"testing"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/serialize"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// The pull dispatch logic is tested in the pull package. These tests cover
// how connections wires it to its streams, gates and namespace.

const pullActorType = "dapr.internal.default.myapp.activity"

// pullHarness is a connections loop with one pull-eligible sidecar recording
// what its stream was handed. Events the loop enqueues to itself are queued
// and applied by pump on the test goroutine.
type pullHarness struct {
	c          *connections
	events     chan loops.EventConn
	dispatched []*loops.TriggerRequest
	closes     int
}

func newPullHarness(t *testing.T) *pullHarness {
	t.Helper()

	h := &pullHarness{
		c:      connsCache.New().(*connections),
		events: make(chan loops.EventConn, 1024),
	}
	h.c.schedulerCount = 1
	h.c.nsLoop = loopfake.New[loops.EventNS]().WithEnqueue(func(e loops.EventNS) {
		if _, ok := e.(*loops.ConnCloseNamespace); ok {
			h.closes++
		}
	})
	h.c.loop = loopfake.New[loops.EventConn]().WithEnqueue(func(e loops.EventConn) {
		h.events <- e
	})
	h.c.pull = h.c.newPull()

	actorAddress := "127.0.0.1:1000"
	req := &schedulerv1pb.WatchJobsRequestInitial{
		AppId:        "myapp",
		Namespace:    "default",
		ActorTypes:   []string{pullActorType},
		ActorAddress: &actorAddress,
		PullSlots:    []*schedulerv1pb.PullSlots{{ActorType: pullActorType, Free: 1}},
	}
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		h.dispatched = append(h.dispatched, e.(*loops.TriggerRequest))
	})
	h.c.streams[0] = h.c.streamPool.Add(store.Options{
		Loop:         streamLoop,
		ActorTypes:   req.GetActorTypes(),
		ActorAddress: &actorAddress,
	})
	h.c.streamLoops[0] = streamLoop
	h.c.streamIDx = 1
	h.c.updateConcurrencyLimits(0, req)
	h.c.pull.AddStream(0, req)
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

// closeStream closes the stream the way the stream loop and connections loop
// do: the connections loop reaps it, then the stream orphans the pull
// triggers it still had in flight.
func (h *pullHarness) closeStream(t *testing.T, inflight ...*loops.TriggerRequest) {
	t.Helper()
	h.c.streams[0] = func() {}
	h.c.handleCloseStream(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "default"})
	for _, req := range inflight {
		req.Orphan()
	}
	h.pump(t)
}

func pullTrigger(actorID, name string) *loops.TriggerRequest {
	req := makeTriggerRequest(pullActorType, actorID, name)
	req.Job.Name = "run-activity"
	req.Job.Key = serialize.ActorReminderKey("default", pullActorType, actorID, "run-activity")
	req.Job.Metadata.Pull = new(true)
	req.Job.Metadata.Namespace = "default"
	req.Job.Metadata.AppId = "myapp"
	req.ResultFn = func(api.TriggerResponseResult) {}
	return req
}

func TestPullDispatch_FallsBackToRegularRouting(t *testing.T) {
	t.Parallel()

	var dispatched []loops.EventStream
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		dispatched = append(dispatched, e)
	})
	c := connsCache.New().(*connections)
	c.schedulerCount = 1
	c.pull = c.newPull()
	c.streamPool.Add(store.Options{Loop: streamLoop, ActorTypes: []string{pullActorType}})

	req := pullTrigger("wf::1::0", "act")
	c.handleTriggerRequest(req)

	require.Len(t, dispatched, 1, "a pull trigger with no sidecar reporting slots uses the regular actor routing")
	assert.Same(t, req, dispatched[0].(*loops.TriggerRequest))
}

func TestPullDispatch_NamespaceKeptWhileFlightsHeld(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t)

	running := pullTrigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	require.Len(t, h.dispatched, 1)
	h.closeStream(t, running)
	assert.Equal(t, 0, h.closes, "the loop holding an orphaned pull trigger stays for its sidecar to reattach")

	running.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
	assert.Equal(t, 1, h.closes, "the loop closes once the last held trigger resolves")
}

func TestPullDispatch_HeldGatesSurviveTheStream(t *testing.T) {
	t.Parallel()
	h := newPullHarness(t)
	limit := pullActorType + ":act"
	h.c.updateConcurrencyLimits(0, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{{
			MaxConcurrent: 1,
			Name:          new("act"),
			Target:        &schedulerv1pb.ConcurrencyLimit_Actor{Actor: &schedulerv1pb.ConcurrencyLimitActor{Type: pullActorType}},
		}},
	})

	running := pullTrigger("wf::1::0", "act")
	h.c.handleTriggerRequest(running)
	require.Len(t, h.dispatched, 1)
	require.Equal(t, uint32(1), h.c.concurrencyGates[limit].current)

	h.closeStream(t, running)
	gate, ok := h.c.concurrencyGates[limit]
	require.True(t, ok, "a gate held by a running activity outlives the stream that delivered it")
	assert.Equal(t, uint32(1), gate.current)

	running.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
	assert.Equal(t, uint32(0), gate.current, "the gate is released when the activity resolves")
	assert.NotContains(t, h.c.concurrencyGates, limit, "a gate no stream sets goes once no activity holds it")
}
