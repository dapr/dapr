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
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

var t0 = time.Unix(1_000_000, 0)

// window feeds an estimator one window of acknowledgements, n per name with
// the given round trip time, and evaluates it at now.
type window struct {
	rtts  map[string]time.Duration
	n     int
	acks  uint32
	bound bool
	in    evalInput
}

func (w window) run(e *estimator, now time.Time) (bool, bool) {
	n := w.n
	if n == 0 {
		n = minSamples
	}
	for name, rtt := range w.rtts {
		for i := range n {
			sent := e.windowStart.Add(time.Duration(i) * time.Microsecond)
			e.record(name, sent, sent.Add(rtt))
		}
	}
	e.acks += w.acks
	if w.bound {
		e.lastBound = now.Add(-time.Millisecond)
	}
	w.in.now = now
	w.in.ratio, w.in.voters = e.latencyRatio(e.changedAt)
	if w.in.serviceTime == 0 {
		w.in.serviceTime = e.uncongested()
	}
	changed, overload := e.control(w.in)
	e.roll()
	return changed, overload
}

func ms(d int) time.Duration { return time.Duration(d) * time.Millisecond }

func baselined(t *testing.T, names ...string) *estimator {
	t.Helper()
	e := newEstimator(t0, 1)
	rtts := make(map[string]time.Duration)
	for _, name := range names {
		rtts[name] = ms(20)
	}
	changed, _ := window{rtts: rtts}.run(e, t0.Add(time.Second))
	require.False(t, changed)
	return e
}

func TestEstimator_NoLimitBelowRatio(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a", "b")
	changed, _ := window{rtts: map[string]time.Duration{"a": ms(30), "b": ms(39)}, in: evalInput{inflight: 100}}.run(e, t0.Add(2*time.Second))
	assert.False(t, changed)
	assert.Equal(t, uint32(0), e.limit)
}

func TestEstimator_CreatesLimitOnOverload(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a", "b")
	changed, overload := window{rtts: map[string]time.Duration{"a": ms(100), "b": ms(100)}, in: evalInput{inflight: 100}}.run(e, t0.Add(2*time.Second))
	assert.True(t, changed)
	assert.True(t, overload)
	assert.Equal(t, uint32(75), e.limit)
}

func TestEstimator_OneSlowNameIsOutvoted(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a", "b", "c")
	changed, _ := window{rtts: map[string]time.Duration{"a": ms(400), "b": ms(20), "c": ms(22)}, in: evalInput{inflight: 100}}.run(e, t0.Add(2*time.Second))
	assert.False(t, changed)
}

func TestEstimator_NewSlowNameDoesNotVote(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a", "b")
	rtts := map[string]time.Duration{"a": ms(20), "b": ms(20), "batch": ms(500)}
	changed, _ := window{rtts: rtts, in: evalInput{inflight: 100}}.run(e, t0.Add(2*time.Second))
	assert.False(t, changed, "a name with no history has no baseline")
	changed, _ = window{rtts: rtts, in: evalInput{inflight: 100}}.run(e, t0.Add(3*time.Second))
	assert.False(t, changed, "a name that is slow from the start has a slow baseline")
}

func TestEstimator_SmallBaselinesAreFloored(t *testing.T) {
	t.Parallel()
	e := newEstimator(t0, 1)
	window{rtts: map[string]time.Duration{"a": 100 * time.Microsecond}}.run(e, t0.Add(time.Second))
	changed, _ := window{rtts: map[string]time.Duration{"a": ms(1)}, in: evalInput{inflight: 100}}.run(e, t0.Add(2*time.Second))
	assert.False(t, changed, "1ms is not overload against a 100us baseline")
}

func TestEstimator_ProbeKeptOrReverted(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a")
	now := t0.Add(2 * time.Second)
	_, overload := window{rtts: map[string]time.Duration{"a": ms(100)}, in: evalInput{inflight: 100}}.run(e, now)
	require.True(t, overload)
	require.Equal(t, uint32(75), e.limit)

	normal := map[string]time.Duration{"a": ms(20)}

	// No probe while held.
	now = now.Add(5 * time.Second)
	changed, _ := window{rtts: normal, acks: 75 * 5, bound: true}.run(e, now)
	assert.False(t, changed)

	now = now.Add(5 * time.Second)
	changed, _ = window{rtts: normal, acks: 75 * 5, bound: true}.run(e, now)
	require.True(t, changed)
	assert.Equal(t, uint32(93), e.limit, "probe by 25 percent")

	now = now.Add(time.Second)
	changed, _ = window{rtts: normal, acks: 93, bound: true}.run(e, now)
	assert.False(t, changed, "throughput followed, the probe stands")
	assert.Equal(t, uint32(93), e.limit)

	now = now.Add(time.Second)
	changed, _ = window{rtts: normal, acks: 93, bound: true}.run(e, now)
	require.True(t, changed)
	assert.Equal(t, uint32(116), e.limit)

	now = now.Add(time.Second)
	changed, _ = window{rtts: normal, acks: 95, bound: true}.run(e, now)
	assert.True(t, changed)
	assert.Equal(t, uint32(93), e.limit, "throughput did not follow, the probe is reverted")

	now = now.Add(time.Second)
	changed, _ = window{rtts: normal, acks: 93, bound: true}.run(e, now)
	assert.False(t, changed, "held after a failed probe")
}

func TestEstimator_CutOnOverloadWhileLimited(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a")
	now := t0.Add(2 * time.Second)
	window{rtts: map[string]time.Duration{"a": ms(100)}, in: evalInput{inflight: 100}}.run(e, now)
	require.Equal(t, uint32(75), e.limit)

	now = now.Add(time.Second)
	_, overload := window{rtts: map[string]time.Duration{"a": ms(60)}, bound: true}.run(e, now)
	assert.True(t, overload)
	assert.Equal(t, uint32(56), e.limit)
}

func TestEstimator_SamplesFromBeforeAChangeDoNotVote(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a")
	now := t0.Add(2 * time.Second)
	window{rtts: map[string]time.Duration{"a": ms(100)}, in: evalInput{inflight: 100}}.run(e, now)
	require.Equal(t, uint32(75), e.limit)

	// Triggers sent before the limit was created complete slowly.
	for i := range 20 {
		sent := now.Add(-time.Duration(i+1) * time.Millisecond)
		e.record("a", sent, now.Add(500*time.Millisecond))
	}
	in := evalInput{now: now.Add(time.Second)}
	in.ratio, in.voters = e.latencyRatio(e.changedAt)
	changed, _ := e.control(in)
	assert.False(t, changed)
	assert.Equal(t, uint32(75), e.limit)
}

func TestEstimator_ReleasedWhenNotBinding(t *testing.T) {
	t.Parallel()
	e := baselined(t, "a")
	now := t0.Add(2 * time.Second)
	window{rtts: map[string]time.Duration{"a": ms(100)}, in: evalInput{inflight: 100}}.run(e, now)
	require.Equal(t, uint32(75), e.limit)

	normal := map[string]time.Duration{"a": ms(20)}
	now = now.Add(30 * time.Second)
	changed, _ := window{rtts: normal}.run(e, now)
	assert.False(t, changed)
	now = now.Add(30 * time.Second)
	changed, _ = window{rtts: normal}.run(e, now)
	assert.True(t, changed)
	assert.Equal(t, uint32(0), e.limit)
}

func TestEstimator_FloorAndCeiling(t *testing.T) {
	t.Parallel()

	e := baselined(t, "a")
	window{rtts: map[string]time.Duration{"a": ms(100)}, in: evalInput{inflight: 2, floor: 3}}.run(e, t0.Add(2*time.Second))
	assert.Equal(t, uint32(3), e.limit, "never below the floor")

	e = baselined(t, "a")
	now := t0.Add(2 * time.Second)
	window{rtts: map[string]time.Duration{"a": ms(100)}, in: evalInput{inflight: 100, ceiling: 10}}.run(e, now)
	assert.Equal(t, uint32(10), e.limit, "never above the ceiling")

	now = now.Add(probeHold)
	changed, _ := window{rtts: map[string]time.Duration{"a": ms(20)}, acks: 100, bound: true, in: evalInput{ceiling: 10}}.run(e, now)
	assert.False(t, changed, "no probe past the ceiling")
	assert.Equal(t, uint32(10), e.limit)
}

func TestEstimator_PeriodFollowsRoundTripTime(t *testing.T) {
	t.Parallel()
	assert.Equal(t, minEvalPeriod, evalPeriod(ms(10)))
	assert.Equal(t, 6*time.Second, evalPeriod(3*time.Second))
	assert.Equal(t, maxEvalPeriod, evalPeriod(time.Minute))
}

// burst triggers n triggers of each actor type, advances the clock by rtt
// and acknowledges acks of each with result, leaving the rest in flight.
func (h *harness) burst(prefix string, actorTypes []string, n, acks int, rtt time.Duration, result api.TriggerResponseResult) {
	h.t.Helper()
	for _, actorType := range actorTypes {
		for i := range n {
			h.trigger(prefix+actorType+itoa(i), actorType, "wf")
		}
	}
	h.clock.Step(rtt)
	for _, actorType := range actorTypes {
		for i := range acks {
			h.ack(prefix+actorType+itoa(i), result)
		}
	}
}

func (h *harness) nextWindow() {
	h.clock.Step(time.Second)
}

func TestAdaptive_TypeLimitFromAcknowledgements(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA}, nil)

	h.burst("base-", []string{wfTypeA}, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.burst("load-", []string{wfTypeA}, 200, 20, ms(200), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.trigger("tick", wfTypeA, "wf")

	gate := h.c.concurrencyGates[wfTypeA]
	require.NotNil(t, gate.est)
	assert.Equal(t, uint32(1), gate.est.limit, "Little's law: 20 acks in 1.2s at 20ms uncongested is under one in flight")
	assert.Equal(t, uint32(0), h.c.nsGate.est.limit, "one app alone does not limit the namespace")
	assert.Equal(t, 1, h.c.pending.size, "the trigger after the limit waits")
}

func TestAdaptive_NamespaceLimitWhenAppsSlowTogether(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA}, nil)
	h.connect("10.0.0.2:1", []string{wfTypeB}, nil)
	types := []string{wfTypeA, wfTypeB}

	h.burst("base-", types, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.burst("load-", types, 100, 20, ms(200), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.trigger("tick", wfTypeA, "wf")

	assert.Equal(t, uint32(2), h.c.nsGate.est.limit, "the knee is under one in flight, so the floor of one per app")
	assert.Equal(t, uint32(0), h.c.concurrencyGates[wfTypeA].est.limit, "apps stand down while the namespace is overloaded")
	assert.Equal(t, uint32(0), h.c.concurrencyGates[wfTypeB].est.limit, "apps stand down while the namespace is overloaded")
}

func TestAdaptive_FailedAcknowledgementsCarryNoLatency(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA}, nil)
	h.burst("f-", []string{wfTypeA}, 20, 20, ms(500), api.TriggerResponseResult_FAILED)

	gate := h.c.concurrencyGates[wfTypeA]
	assert.Empty(t, gate.est.names)
	assert.Equal(t, uint32(0), gate.est.acks)
	assert.Equal(t, uint32(0), gate.current, "failed triggers still release their slots")
}

func TestAdaptive_NamespaceRefusalDoesNotBindTheActorType(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA}, &schedulerv1pb.WatchJobsRequestInitial{})
	h.c.nsGate.est.limit = 1
	h.c.concurrencyGates[wfTypeA].est.limit = 10

	h.trigger("a", wfTypeA, "wf")
	h.trigger("b", wfTypeA, "wf")

	assert.Equal(t, h.clock.Now(), h.c.nsGate.est.lastBound)
	assert.True(t, h.c.concurrencyGates[wfTypeA].est.lastBound.IsZero())
}

func TestAdaptive_OneOfTwoSlowAppsLimitsOnlyItself(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA}, nil)
	h.connect("10.0.0.2:1", []string{wfTypeB}, nil)

	h.burst("base-", []string{wfTypeA, wfTypeB}, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.burst("fast-", []string{wfTypeB}, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.burst("load-", []string{wfTypeA}, 200, 20, ms(200), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.trigger("tick", wfTypeB, "wf")

	assert.Equal(t, uint32(0), h.c.nsGate.est.limit, "one slow app of two is outvoted at the namespace")
	assert.Equal(t, uint32(1), h.c.concurrencyGates[wfTypeA].est.limit)
	assert.Equal(t, uint32(0), h.c.concurrencyGates[wfTypeB].est.limit)
}

func TestEstimator_BacksOffToTheKnee(t *testing.T) {
	t.Parallel()

	e := baselined(t, "a")
	// 200 acknowledgements a second at a 20ms uncongested round trip is 4
	// in flight at the knee, 6 with kneeGain.
	_, overload := window{rtts: map[string]time.Duration{"a": ms(1000)}, acks: 200, in: evalInput{inflight: 300}}.run(e, t0.Add(2*time.Second))
	require.True(t, overload)
	assert.Equal(t, uint32(6), e.limit)
}

func TestEstimator_UncongestedIsWeightedByCompletions(t *testing.T) {
	t.Parallel()

	e := baselined(t, "fast")
	window{rtts: map[string]time.Duration{"fast": ms(20), "slow": ms(200)}}.run(e, t0.Add(2*time.Second))
	for i := range 30 {
		e.record("fast", t0, t0.Add(ms(20)))
		if i < 10 {
			e.record("slow", t0, t0.Add(ms(200)))
		}
	}
	assert.Equal(t, ms(65), e.uncongested(), "(30*20ms + 10*200ms) / 40")
}

func TestAdaptive_OneAppIsOneNamespaceVoter(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA, actTypeA}, nil)
	types := []string{wfTypeA, actTypeA}

	h.burst("base-", types, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.burst("load-", types, 200, 20, ms(200), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.trigger("tick", wfTypeA, "wf")

	assert.Equal(t, uint32(0), h.c.nsGate.est.limit, "an app's workflows and activities slowing together is still one app")
	assert.Positive(t, h.c.concurrencyGates[wfTypeA].est.limit)
	assert.Positive(t, h.c.concurrencyGates[actTypeA].est.limit)
}

func TestAdaptive_NamespaceTakesOverFromAppLimits(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA}, nil)
	h.connect("10.0.0.2:1", []string{wfTypeB}, nil)
	types := []string{wfTypeA, wfTypeB}

	h.burst("base-", types, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.burst("load-", types, 200, 20, ms(200), api.TriggerResponseResult_SUCCESS)
	// App a noticed the slowdown a pass before the namespace did.
	h.c.concurrencyGates[wfTypeA].est.limit = 300
	h.nextWindow()
	h.trigger("tick", wfTypeB, "wf")

	assert.Positive(t, h.c.nsGate.est.limit)
	assert.Equal(t, uint32(0), h.c.concurrencyGates[wfTypeA].est.limit, "the namespace takes over from the app's own limit")
	assert.Equal(t, uint32(0), h.c.concurrencyGates[wfTypeB].est.limit)
}

func TestAdaptive_AppsStandDownUnderANamespaceLimit(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)
	h.connect("10.0.0.1:1", []string{wfTypeA}, nil)
	h.connect("10.0.0.2:1", []string{wfTypeB}, nil)

	h.burst("base-", []string{wfTypeA, wfTypeB}, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.c.nsGate.est.limit = 1000
	h.nextWindow()
	h.burst("fast-", []string{wfTypeB}, 20, 20, ms(20), api.TriggerResponseResult_SUCCESS)
	h.burst("load-", []string{wfTypeA}, 200, 20, ms(200), api.TriggerResponseResult_SUCCESS)
	h.nextWindow()
	h.trigger("tick", wfTypeB, "wf")

	assert.Equal(t, uint32(0), h.c.concurrencyGates[wfTypeA].est.limit, "an app does not derive its own limit under a namespace limit")
}
