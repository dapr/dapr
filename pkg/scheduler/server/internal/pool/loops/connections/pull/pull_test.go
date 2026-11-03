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

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

const (
	testActorType = "dapr.internal.default.myapp.activity"
	testAppID     = "myapp"
)

// harness is a Dispatcher with n pull-eligible sidecars, one stream each,
// recording what each stream was handed. Shared gates are a small map, as in
// connections. Events the Dispatcher queues to its loop are applied by pump
// on the test goroutine.
type harness struct {
	d          *Dispatcher
	events     chan loops.EventConn
	streams    map[uint64]loop.Interface[loops.EventStream]
	nextIdx    uint64
	dispatched [][]*loops.TriggerRequest
	results    map[*loops.TriggerRequest]api.TriggerResponseResult
	gates      map[string]*gate
	// routed holds the triggers handed back for regular routing.
	routed []*loops.TriggerRequest
	// acquires counts gate acquisition attempts.
	acquires int
}

type gate struct {
	limit, current uint32
}

func newHarness(t *testing.T, streams int, slots uint32) *harness {
	t.Helper()

	h := &harness{
		events:     make(chan loops.EventConn, 1024),
		streams:    make(map[uint64]loop.Interface[loops.EventStream]),
		dispatched: make([][]*loops.TriggerRequest, streams),
		results:    make(map[*loops.TriggerRequest]api.TriggerResponseResult),
		gates:      make(map[string]*gate),
	}
	h.d = New(Options{
		Loop: loopfake.New[loops.EventConn]().WithEnqueue(func(e loops.EventConn) {
			h.events <- e
		}),
		StreamLoop: func(streamIDx uint64) (loop.Interface[loops.EventStream], bool) {
			l, ok := h.streams[streamIDx]
			return l, ok
		},
		AcquireGates: h.acquire,
		ReleaseGates: h.release,
		Requeue:      h.handle,
		MaxParked:    100,
	})

	for i := range streams {
		h.addStream(t, i, slots)
	}
	return h
}

// gateKeys mirrors connections: the actor type gate, then the per-name gate,
// for those that exist.
func (h *harness) gateKeys(req *loops.TriggerRequest) []string {
	meta := req.Job.GetMetadata()
	typeKey := meta.GetTarget().GetActor().GetType()
	var keys []string
	if _, ok := h.gates[typeKey]; ok {
		keys = append(keys, typeKey)
	}
	if meta.ConcurrencyKey != nil {
		if named := typeKey + ":" + meta.GetConcurrencyKey(); h.gates[named] != nil {
			keys = append(keys, named)
		}
	}
	return keys
}

func (h *harness) acquire(req *loops.TriggerRequest) ([]string, string, bool) {
	h.acquires++
	keys := h.gateKeys(req)
	for _, key := range keys {
		if g := h.gates[key]; g.current >= g.limit {
			return nil, key, false
		}
	}
	for _, key := range keys {
		h.gates[key].current++
	}
	return keys, "", true
}

// release frees gates and drains parked work, as connections does.
func (h *harness) release(keys []string) {
	for _, key := range keys {
		if g, ok := h.gates[key]; ok && g.current > 0 {
			g.current--
		}
	}
	h.d.Drain()
}

// handle routes a trigger as connections does: pull first, otherwise the
// regular route, recorded in routed.
func (h *harness) handle(req *loops.TriggerRequest) {
	if !h.d.HandleTrigger(req) {
		h.routed = append(h.routed, req)
	}
}

// pump applies every event the Dispatcher has queued to its loop.
func (h *harness) pump(t *testing.T) {
	t.Helper()
	for {
		select {
		case e := <-h.events:
			e.(*loops.ConnRun).Fn()
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

// sidecarOf is the Dispatcher's ID for the sidecar at addr(i).
func sidecarOf(i int) string {
	return sidecarID(0, testAppID, addr(i))
}

func key(actorID string) string {
	return "actorreminder||default||" + testActorType + "||" + actorID + "||run-activity"
}

func (h *harness) pool(i int) *pool {
	return h.d.poolOf(testActorType, sidecarOf(i))
}

// addStream connects a stream recording into dispatched[i] for its own
// sidecar, offering slots.
func (h *harness) addStream(t *testing.T, i int, slots uint32) uint64 {
	t.Helper()
	return h.addStreamFor(t, i, addr(i), &schedulerv1pb.WatchJobsRequestInitial{
		PullSlots: []*schedulerv1pb.PullSlots{{ActorType: testActorType, Free: slots}},
	})
}

// addStreamFor connects a stream recording into dispatched[i] for the
// sidecar at actorAddress with the given initial request, as the connections
// loop does.
func (h *harness) addStreamFor(t *testing.T, i int, actorAddress string, req *schedulerv1pb.WatchJobsRequestInitial) uint64 {
	t.Helper()
	idx := h.nextIdx
	h.nextIdx++

	req.AppId = testAppID
	req.Namespace = "default"
	req.ActorTypes = []string{testActorType}
	req.ActorAddress = &actorAddress
	h.streams[idx] = loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		h.dispatched[i] = append(h.dispatched[i], e.(*loops.TriggerRequest))
	})
	h.d.AddStream(idx, req)
	h.d.Drain()
	return idx
}

// closeStream closes stream idx the way the connections loop and stream loop
// do: the connections loop reaps the stream, then the stream resolves the
// triggers it still had in flight undeliverable.
func (h *harness) closeStream(t *testing.T, idx uint64, inflight ...*loops.TriggerRequest) {
	t.Helper()
	delete(h.streams, idx)
	h.d.CloseStream(idx)
	h.d.Drain()
	for _, req := range inflight {
		req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
	}
	h.pump(t)
}

func (h *harness) trigger(actorID, name string) *loops.TriggerRequest {
	req := &loops.TriggerRequest{Job: &internalsv1pb.JobEvent{
		Key:  key(actorID),
		Name: "run-activity",
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default",
			AppId:     testAppID,
			Pull:      new(true),
			Target: &schedulerv1pb.JobTargetMetadata{Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: testActorType, Id: actorID},
			}},
		},
	}}
	if name != "" {
		req.Job.Metadata.ConcurrencyKey = &name
	}
	req.ResultFn = func(r api.TriggerResponseResult) { h.results[req] = r }
	return req
}

// ack completes a dispatched trigger the way the stream loop does when the
// sidecar reports the result. It does not free a slot: only a report does.
func (h *harness) ack(t *testing.T, req *loops.TriggerRequest) {
	t.Helper()
	req.ResultFn(api.TriggerResponseResult_SUCCESS)
	h.pump(t)
}

// report delivers a free slot count reported on sidecar i's newest stream.
func (h *harness) report(i int, free uint32) {
	var idx uint64
	if p := h.pool(i); p != nil {
		idx = p.streams[len(p.streams)-1]
	}
	h.reportOn(idx, i, free)
}

// reportOn delivers a free slot count reported by sidecar i on stream idx.
func (h *harness) reportOn(idx uint64, i int, free uint32) {
	h.d.HandleSlots(&loops.PullSlotsUpdate{StreamIDx: idx, AppID: testAppID, ActorAddress: addr(i), ActorType: testActorType, Free: free})
}

// resultOf returns req's result, failing if the trigger was never resolved:
// SUCCESS is the zero value, so a plain map read cannot tell.
func (h *harness) resultOf(t *testing.T, req *loops.TriggerRequest) api.TriggerResponseResult {
	t.Helper()
	r, ok := h.results[req]
	require.True(t, ok, "the trigger was never resolved")
	return r
}

func (h *harness) totalDispatched() int {
	n := 0
	for _, d := range h.dispatched {
		n += len(d)
	}
	return n
}

func TestDispatcher_ShutdownReturnsPendingAndInflight(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1, 1)

	running := h.trigger("wf::1::0", "act")
	h.handle(running)
	parked := h.trigger("wf::2::0", "act")
	h.handle(parked)

	h.d.Shutdown()
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.resultOf(t, parked))
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, h.resultOf(t, running))
}
