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
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	loopfake "github.com/dapr/kit/events/loop/fake"
	"github.com/dapr/kit/ptr"
)

const (
	wfTypeA  = "dapr.internal.default.a.workflow"
	actTypeA = "dapr.internal.default.a.activity"
	wfTypeB  = "dapr.internal.default.b.workflow"
)

// harness drives a connections loop through Handle with fake stream loops,
// recording what is dispatched and how each trigger is resolved.
type harness struct {
	t     *testing.T
	c     *connections
	clock *clocktesting.FakeClock

	dispatched []*loops.TriggerRequest
	results    map[string]api.TriggerResponseResult
	releases   []*loops.ConcurrencyRelease
	streamIDx  uint64
}

func newHarness(t *testing.T, adaptive bool) *harness {
	t.Helper()
	h := &harness{
		t:       t,
		clock:   clocktesting.NewFakeClock(time.Unix(1_000_000, 0)),
		results: make(map[string]api.TriggerResponseResult),
	}
	h.c = new(connections)
	h.c.reset(Options{Namespace: "default", Adaptive: adaptive, Clock: h.clock})
	h.c.loop = loopfake.New[loops.EventConn]().WithEnqueue(func(e loops.EventConn) {
		h.releases = append(h.releases, e.(*loops.ConcurrencyRelease))
	})
	return h
}

// connect registers a stream hosting actorTypes with the given declarations.
func (h *harness) connect(addr string, actorTypes []string, req *schedulerv1pb.WatchJobsRequestInitial) {
	h.t.Helper()
	if req == nil {
		req = new(schedulerv1pb.WatchJobsRequestInitial)
	}
	req.ActorTypes = actorTypes
	if addr != "" {
		req.ActorAddress = ptr.Of(addr)
	}
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		h.dispatched = append(h.dispatched, e.(*loops.TriggerRequest))
	})
	idx := h.streamIDx
	h.streamIDx++
	h.c.streams[idx] = h.c.streamPool.Add(store.Options{
		Loop:         streamLoop,
		ActorTypes:   actorTypes,
		ActorAddress: req.ActorAddress,
	})
	h.c.now = h.clock.Now()
	h.c.registerStream(idx, req)
	h.c.drain()
}

func (h *harness) trigger(id, actorType, name string) {
	h.t.Helper()
	meta := &schedulerv1pb.JobMetadata{
		Namespace: "default",
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: actorType, Id: id},
			},
		},
	}
	if name != "" {
		meta.ConcurrencyKey = ptr.Of(name)
	}
	req := &loops.TriggerRequest{
		Job: &internalsv1pb.JobEvent{Name: id, Metadata: meta},
		ResultFn: func(r api.TriggerResponseResult) {
			h.results[id] = r
		},
	}
	require.NoError(h.t, h.c.Handle(context.Background(), req))
}

// ack resolves the dispatched trigger id with result and handles the
// release it produces.
func (h *harness) ack(id string, result api.TriggerResponseResult) {
	h.t.Helper()
	for _, req := range h.dispatched {
		if req.Job.GetName() == id {
			h.releases = nil
			req.ResultFn(result)
			require.Len(h.t, h.releases, 1)
			require.NoError(h.t, h.c.Handle(context.Background(), h.releases[0]))
			return
		}
	}
	require.Failf(h.t, "not dispatched", "trigger %s", id)
}

func (h *harness) names() []string {
	out := make([]string, len(h.dispatched))
	for i, req := range h.dispatched {
		out[i] = req.Job.GetName()
	}
	return out
}

// serve acks the oldest unacked dispatch n times, returning the names
// dispatched as a result.
func (h *harness) serve(n int) []string {
	h.t.Helper()
	acked := make(map[string]bool)
	before := len(h.dispatched)
	for range n {
		for _, req := range h.dispatched {
			id := req.Job.GetName()
			if _, done := h.results[id]; !done && !acked[id] {
				acked[id] = true
				h.ack(id, api.TriggerResponseResult_SUCCESS)
				break
			}
		}
	}
	return h.names()[before:]
}

func limit(actorType string, name *string, maxConcurrent int32) *schedulerv1pb.ConcurrencyLimit {
	return &schedulerv1pb.ConcurrencyLimit{
		Target:        &schedulerv1pb.ConcurrencyLimit_Actor{Actor: &schedulerv1pb.ConcurrencyLimitActor{Type: actorType}},
		Name:          name,
		MaxConcurrent: maxConcurrent,
	}
}

func count(names []string, prefix string) int {
	var n int
	for _, name := range names {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

func TestPending_StrideFollowsNiceness(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{wfTypeA}, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:             "a",
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit(wfTypeA, nil, 1)},
		Niceness: &schedulerv1pb.WorkflowNiceness{
			Default:   10,
			Workflows: map[string]int32{"hi": -10},
		},
	})

	h.trigger("hold", wfTypeA, "")
	for i := range 100 {
		h.trigger("lo-"+itoa(i), wfTypeA, "lo")
	}
	for i := range 100 {
		h.trigger("hi-"+itoa(i), wfTypeA, "hi")
	}
	require.Equal(t, 200, h.c.pending.size)

	got := h.serve(90)

	// lo started waiting first so wins the opening tie, then hi's stride of
	// 1048576/9548 = 109 against lo's 1048576/110 = 9532 gives hi 88 turns.
	assert.Equal(t, "lo-0", got[0])
	assert.Equal(t, 88, count(got[1:89], "hi-"))
	assert.Equal(t, "lo-1", got[89])
	assert.Equal(t, "hi-0", got[1], "FIFO within a flow")
	assert.Equal(t, "hi-87", got[88], "FIFO within a flow")
}

func TestPending_EqualNicenessAlternates(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{"mytype"}, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit("mytype", nil, 1)},
	})

	h.trigger("hold", "mytype", "")
	for _, id := range []string{"a1", "a2", "a3"} {
		h.trigger(id, "mytype", "a")
	}
	for _, id := range []string{"b1", "b2", "b3"} {
		h.trigger(id, "mytype", "b")
	}

	assert.Equal(t, []string{"a1", "b1", "a2", "b2", "a3", "b3"}, h.serve(6))
}

func TestPending_BlockedFlowIsNotCharged(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{"mytype"}, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{
			limit("mytype", nil, 2),
			limit("mytype", ptr.Of("x"), 1),
		},
	})

	h.trigger("hold-x", "mytype", "x")
	h.trigger("hold", "mytype", "")
	h.trigger("x1", "mytype", "x")
	h.trigger("y1", "mytype", "y")
	h.trigger("y2", "mytype", "y")
	require.Equal(t, []string{"hold-x", "hold"}, h.names())

	h.ack("hold", api.TriggerResponseResult_SUCCESS)
	assert.Equal(t, "y1", h.names()[2], "x1 is blocked on its own gate, so y1 takes the slot")

	flowX := h.c.pendingFlow([3]string{"mytype", "mytype", "x"})
	require.NotNil(t, flowX)
	assert.Equal(t, uint64(0), flowX.pass, "a skipped flow is not charged")

	h.ack("hold-x", api.TriggerResponseResult_SUCCESS)
	assert.Equal(t, "x1", h.names()[3], "the elder flow keeps its place")
}

func TestPending_ReleaseOfTwoGatesDispatchesAtOnce(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{"mytype"}, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{
			limit("mytype", nil, 2),
			limit("mytype", ptr.Of("hot"), 1),
		},
	})

	h.trigger("h1", "mytype", "hot")
	h.trigger("h2", "mytype", "")
	h.trigger("w1", "mytype", "hot")
	require.Equal(t, []string{"h1", "h2"}, h.names())

	// One acknowledgement frees both the type gate and the named gate w1
	// needs.
	h.ack("h1", api.TriggerResponseResult_SUCCESS)
	assert.Equal(t, []string{"h1", "h2", "w1"}, h.names())
}

func TestPending_AppsShareTheNamespaceByNiceness(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA, actTypeA}, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:    "a",
		Niceness: &schedulerv1pb.WorkflowNiceness{Default: 0},
	})
	h.connect("10.0.0.2:1", []string{wfTypeB}, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:    "b",
		Niceness: &schedulerv1pb.WorkflowNiceness{Default: 10},
	})
	h.c.nsGate.est.limit = 1

	h.trigger("hold", wfTypeA, "")
	for i := range 60 {
		h.trigger("a-wf-"+itoa(i), wfTypeA, "")
		h.trigger("a-act-"+itoa(i), actTypeA, "")
		h.trigger("b-"+itoa(i), wfTypeB, "")
	}

	got := h.serve(100)
	b := count(got, "b-")
	assert.InDelta(t, 100*110/1134.0, b, 2, "app b gets its weight's share, whichever kinds app a has pending")
	assert.InDelta(t, count(got, "a-wf-"), count(got, "a-act-"), 1, "an app's actor types share its slots equally")
}

func TestPending_NewFlowStartsAtVirtualClock(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{"mytype"}, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit("mytype", nil, 1)},
	})

	h.trigger("hold", "mytype", "")
	for i := range 10 {
		h.trigger("a"+itoa(i), "mytype", "a")
	}
	h.serve(5)
	for i := range 5 {
		h.trigger("b"+itoa(i), "mytype", "b")
	}

	got := h.serve(4)
	assert.Equal(t, []string{"b0", "a5", "b1", "a6"}, got, "a flow that starts waiting late has no credit banked")
}

func TestPending_WaitingTriggersAreNeverFailed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{"mytype"}, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit("mytype", nil, 1)},
	})
	for i := range 200_000 {
		h.trigger("t"+itoa(i), "mytype", "")
	}

	assert.Len(t, h.dispatched, 1)
	assert.Equal(t, 199_999, h.c.pending.size)
	assert.Empty(t, h.results, "no trigger is resolved while it waits")
}

func TestPending_LostActorTypeIsUndeliverable(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{"mytype"}, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit("mytype", nil, 1)},
	})
	h.trigger("hold", "mytype", "")
	h.trigger("p1", "mytype", "a")
	h.trigger("p2", "mytype", "b")
	require.Equal(t, 2, h.c.pending.size)

	h.c.streams[0]()
	delete(h.c.streams, 0)
	h.c.unregisterStream(0)

	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results["p1"])
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.results["p2"])
	assert.Equal(t, 0, h.c.pending.size)
	assert.Empty(t, h.c.pending.children)
}

func TestPending_HostLimitsAreSummedPerHost(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	hostLimits := &schedulerv1pb.WatchJobsRequestInitial{
		HostConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit(wfTypeA, nil, 2)},
	}
	h.connect("10.0.0.1:1", []string{wfTypeA}, hostLimits)
	// A second stream from the same host does not double its limit.
	h.connect("10.0.0.1:1", []string{wfTypeA}, &schedulerv1pb.WatchJobsRequestInitial{
		HostConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit(wfTypeA, nil, 2)},
	})
	h.connect("10.0.0.2:1", []string{wfTypeA}, &schedulerv1pb.WatchJobsRequestInitial{
		HostConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit(wfTypeA, nil, 2)},
	})

	for i := range 6 {
		h.trigger("t"+itoa(i), wfTypeA, "")
	}
	assert.Len(t, h.dispatched, 4)
	assert.Equal(t, 2, h.c.pending.size)
}

func TestPending_NamespaceGateKeyIsReserved(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("", []string{"dapr.internal.default"}, &schedulerv1pb.WatchJobsRequestInitial{
		ConcurrencyLimits: []*schedulerv1pb.ConcurrencyLimit{limit("dapr.internal.default", nil, 1)},
	})
	assert.NotContains(t, h.c.concurrencyGates, "dapr.internal.default")
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}

func TestPending_NicenessIsScopedToTheStreamsApp(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.connect("", []string{wfTypeA, actTypeA}, &schedulerv1pb.WatchJobsRequestInitial{
		AppId: "a",
		Niceness: &schedulerv1pb.WorkflowNiceness{
			Default:    5,
			Workflows:  map[string]int32{"Checkout": -10},
			Activities: map[string]int32{"Newsletter": 15},
		},
	})

	assert.Equal(t, map[string]int32{
		"dapr.internal.default.a":                     5,
		"dapr.internal.default.a.workflow:Checkout":   -10,
		"dapr.internal.default.a.activity:Newsletter": 15,
	}, h.c.niceness)

	app := [3]string{"dapr.internal.default.a", wfTypeA, "Checkout"}
	assert.Equal(t, weightOf(5), h.c.weightAt(depthApp, app))
	assert.Equal(t, weightOf(-10), h.c.weightAt(depthFlow, app))
	assert.Equal(t, weightOf(5), h.c.weightAt(depthFlow, [3]string{"dapr.internal.default.a", actTypeA, "Other"}), "a flow without its own entry takes the app default")

	h.c.streams[0]()
	delete(h.c.streams, 0)
	h.c.unregisterStream(0)
	assert.Empty(t, h.c.niceness, "niceness goes with the last stream that declared it")
}
