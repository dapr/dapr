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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

const (
	critical   = schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL
	high       = schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_HIGH
	normal     = schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL
	low        = schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW
	background = schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_BACKGROUND
)

// prioritized sets a trigger's level and weight.
func prioritized(req *loops.TriggerRequest, level schedulerv1pb.PriorityLevel, weight float32) *loops.TriggerRequest {
	req.Job.Metadata.PriorityLevel = level.Enum()
	req.Job.Metadata.FairnessWeight = &weight
	return req
}

// accept records the order triggers are offered in and accepts them all.
func accept(order *[]*loops.TriggerRequest) func(*loops.TriggerRequest) bool {
	return func(r *loops.TriggerRequest) bool {
		*order = append(*order, r)
		return true
	}
}

// nameOf is the concurrency key of a dispatched trigger.
func nameOf(req *loops.TriggerRequest) string {
	return req.Job.GetMetadata().GetConcurrencyKey()
}

func TestQueue_InstanceKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "wf-1", instanceKey("wf-1::3::0"))
	assert.Equal(t, "a::b", instanceKey("a::b::3::0"), "instance IDs may contain the separator")
	assert.Equal(t, "plain", instanceKey("plain"))
	assert.Equal(t, "one::two", instanceKey("one::two"), "fewer than two separators is not an activity ID")
}

func TestQueue_LevelOfAndWeightOf(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)
	unset := h.trigger("wf::1::0", "act")
	assert.Equal(t, 2, levelOf(unset), "unset is normal")
	assert.Equal(t, uint64(1000), weightOf(unset), "unset is weight 1, in thousandths")
	assert.Equal(t, uint64(500), weightOf(prioritized(h.trigger("wf::11::0", "act"), normal, 0.5)))
	assert.Equal(t, uint64(1), weightOf(prioritized(h.trigger("wf::12::0", "act"), normal, 0.001)))
	assert.Equal(t, uint64(1000000), weightOf(prioritized(h.trigger("wf::13::0", "act"), normal, 1000)))
	assert.Equal(t, uint64(1000), weightOf(prioritized(h.trigger("wf::14::0", "act"), normal, 0.0001)), "below the range counts as unset")
	assert.Equal(t, uint64(1000), weightOf(prioritized(h.trigger("wf::15::0", "act"), normal, -3)), "negative counts as unset")
	assert.Equal(t, 0, levelOf(prioritized(h.trigger("wf::2::0", "act"), critical, 3)))
	assert.Equal(t, 1, levelOf(prioritized(h.trigger("wf::7::0", "act"), high, 1)))
	assert.Equal(t, 2, levelOf(prioritized(h.trigger("wf::8::0", "act"), normal, 1)))
	assert.Equal(t, 3, levelOf(prioritized(h.trigger("wf::9::0", "act"), low, 1)))
	assert.Equal(t, 4, levelOf(prioritized(h.trigger("wf::3::0", "act"), background, 0)))
	assert.Equal(t, uint64(1000), weightOf(prioritized(h.trigger("wf::4::0", "act"), normal, 0)), "0 is weight 1")
	assert.Equal(t, uint64(1000), weightOf(prioritized(h.trigger("wf::6::0", "act"), normal, 2000)), "above the range counts as unset")
	assert.Equal(t, 2, levelOf(prioritized(h.trigger("wf::5::0", "act"), schedulerv1pb.PriorityLevel(9), 1)), "an unknown level counts as unset")
	assert.Equal(t, 2, levelOf(prioritized(h.trigger("wf::10::0", "act"), schedulerv1pb.PriorityLevel(-1), 1)), "a negative level counts as unset, not a panic")
	assert.Equal(t, []string{"critical", "high", "normal", "low", "background"},
		[]string{levelName(0), levelName(1), levelName(2), levelName(3), levelName(4)})
}

func TestQueue_TryDispatchOneRotates(t *testing.T) {
	t.Parallel()
	q := newQueue(10)

	mk := func(id string) *loops.TriggerRequest {
		return &loops.TriggerRequest{Job: &internalsv1pb.JobEvent{Metadata: &schedulerv1pb.JobMetadata{
			Target: &schedulerv1pb.JobTargetMetadata{Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: testActorType, Id: id},
			}},
		}}}
	}
	a1, a2, a3, b1, c1 := mk("a::1::0"), mk("a::2::0"), mk("a::3::0"), mk("b::1::0"), mk("c::1::0")
	for _, r := range []*loops.TriggerRequest{a1, a2, a3, b1, c1} {
		require.True(t, q.enqueue(r, false))
	}
	require.Equal(t, 5, q.len())

	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []*loops.TriggerRequest{a1, b1, c1, a2, a3}, order)
	assert.Equal(t, 0, q.len())
	assert.Len(t, q.levels[levelOf(a1)].names, 1, "the emptied name keeps its count for a round")
	assert.Zero(t, q.levels[levelOf(a1)].names[""].n)

	// A rejected head does not block another instance's head.
	require.True(t, q.enqueue(a1, false))
	require.True(t, q.enqueue(b1, false))
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

func TestQueue_LevelsAreStrict(t *testing.T) {
	t.Parallel()
	q := newQueue(10)
	h := newHarness(t, 0, 0)
	bg := prioritized(h.trigger("wf::1::0", "act"), background, 1)
	n := h.trigger("wf::2::0", "act")
	lo := prioritized(h.trigger("wf::5::0", "act"), low, 1)
	c := prioritized(h.trigger("wf::3::0", "act"), critical, 1)
	hi := prioritized(h.trigger("wf::4::0", "act"), high, 1)
	for _, r := range []*loops.TriggerRequest{bg, n, lo, c, hi} {
		require.True(t, q.enqueue(r, false))
	}

	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []*loops.TriggerRequest{c, hi, n, lo, bg}, order, "arrival order does not matter across levels, the same name at two levels is two names")

	// A critical head that cannot go does not hold a lower level back.
	require.True(t, q.enqueue(c, false))
	require.True(t, q.enqueue(n, false))
	order = nil
	require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
		order = append(order, r)
		return r == n
	}))
	assert.Equal(t, []*loops.TriggerRequest{c, n}, order)
	assert.Equal(t, 1, q.len())
}

func TestQueue_WeightSharesTurns(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 200 {
		require.True(t, q.enqueue(prioritized(h.trigger("h::"+strconv.Itoa(i)+"::0", "heavy"), normal, 3), false))
		require.True(t, q.enqueue(prioritized(h.trigger("l::"+strconv.Itoa(i)+"::0", "light"), normal, 1), false))
	}

	counts := map[string]int{}
	for range 200 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.InDelta(t, 150, counts["heavy"], 1, "weight 3 : 1 is 3 turns in 4")
	assert.InDelta(t, 50, counts["light"], 1)
}

func TestQueue_WeightRatioHoldsWhenScaled(t *testing.T) {
	t.Parallel()
	q := newQueue(2000)
	h := newHarness(t, 0, 0)
	for i := range 500 {
		require.True(t, q.enqueue(prioritized(h.trigger("a::"+strconv.Itoa(i)+"::0", "six"), normal, 600), false))
		require.True(t, q.enqueue(prioritized(h.trigger("b::"+strconv.Itoa(i)+"::0", "four"), normal, 400), false))
	}

	counts := map[string]int{}
	for range 500 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.InDelta(t, 300, counts["six"], 1, "600 : 400 is the same share as 3 : 2")
	assert.InDelta(t, 200, counts["four"], 1)
}

func TestQueue_FractionalWeight(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 300 {
		require.True(t, q.enqueue(prioritized(h.trigger("a::"+strconv.Itoa(i)+"::0", "half"), normal, 0.5), false))
		require.True(t, q.enqueue(h.trigger("b::"+strconv.Itoa(i)+"::0", "one"), false))
	}

	counts := map[string]int{}
	for range 300 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.InDelta(t, 100, counts["half"], 1, "0.5 against the default 1 is 1 turn in 3")
	assert.InDelta(t, 200, counts["one"], 1)
}

func TestLevel_Rebase(t *testing.T) {
	t.Parallel()

	// Exact arithmetic: every count and startAt drop by the lowest count,
	// so the gaps between names are unchanged.
	lv := &level{names: map[string]*flow{
		"a": {name: "a", dispatchesPerWeight: 5000},
		"b": {name: "b", dispatchesPerWeight: 7500},
		"c": {name: "c", dispatchesPerWeight: 9000},
	}, startAt: 9000}
	lv.rebase()
	assert.Equal(t, uint64(0), lv.names["a"].dispatchesPerWeight)
	assert.Equal(t, uint64(2500), lv.names["b"].dispatchesPerWeight)
	assert.Equal(t, uint64(4000), lv.names["c"].dispatchesPerWeight)
	assert.Equal(t, uint64(4000), lv.startAt)

	// A single name drops to zero, and so does startAt.
	lv = &level{names: map[string]*flow{"only": {name: "only", dispatchesPerWeight: 123456}}, startAt: 123456}
	lv.rebase()
	assert.Equal(t, uint64(0), lv.names["only"].dispatchesPerWeight)
	assert.Equal(t, uint64(0), lv.startAt)

	// With no names only startAt is left, and it drops to zero.
	lv = &level{startAt: 42}
	lv.rebase()
	assert.Equal(t, uint64(0), lv.startAt)

	// Rebasing twice is a no-op the second time.
	lv = &level{names: map[string]*flow{"a": {name: "a", dispatchesPerWeight: 10}, "b": {name: "b", dispatchesPerWeight: 30}}, startAt: 30}
	lv.rebase()
	lv.rebase()
	assert.Equal(t, uint64(0), lv.names["a"].dispatchesPerWeight)
	assert.Equal(t, uint64(20), lv.names["b"].dispatchesPerWeight)
	assert.Equal(t, uint64(20), lv.startAt)
}

func TestQueue_RebaseNotBeforeThreshold(t *testing.T) {
	t.Parallel()
	q := newQueue(100)
	h := newHarness(t, 0, 0)
	require.True(t, q.enqueue(h.trigger("a::1::0", "a"), false))
	require.True(t, q.enqueue(h.trigger("a::2::0", "a"), false))
	lv := &q.levels[levelOf(h.trigger("x::1::0", "a"))]
	lv.names["a"].dispatchesPerWeight = rebaseAt - 2*oneDispatch
	lv.startAt = rebaseAt - 2*oneDispatch

	require.True(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return true }))
	assert.Equal(t, uint64(rebaseAt-oneDispatch), lv.startAt, "one dispatch short of the threshold, no rebase")
	require.True(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return true }))
	assert.Equal(t, uint64(0), lv.startAt, "the dispatch that reaches the threshold rebases the lone name to zero")
}

func TestQueue_RebaseThenReturningNameStartsLow(t *testing.T) {
	t.Parallel()
	q := newQueue(100)
	h := newHarness(t, 0, 0)
	for i := range 3 {
		require.True(t, q.enqueue(h.trigger("s::"+strconv.Itoa(i)+"::0", "stayer"), false))
	}
	lv := &q.levels[levelOf(h.trigger("x::1::0", "stayer"))]
	lv.names["stayer"].dispatchesPerWeight = rebaseAt - 1
	lv.startAt = rebaseAt - 1

	// The first dispatch crosses the threshold and rebases.
	require.True(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return true }))
	require.Less(t, lv.startAt, uint64(rebaseAt))

	// A name that arrives now starts at the rebased startAt, not the old
	// huge value, so it is served in turn with the stayer rather than
	// starving it or being starved.
	require.True(t, q.enqueue(h.trigger("n::1::0", "newcomer"), false))
	assert.Equal(t, lv.startAt, lv.names["newcomer"].dispatchesPerWeight)
	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []string{"stayer", "newcomer", "stayer"}, []string{nameOf(order[0]), nameOf(order[1]), nameOf(order[2])})
}

func TestQueue_RebaseKeepsOrder(t *testing.T) {
	t.Parallel()
	q := newQueue(100)
	h := newHarness(t, 0, 0)
	ahead := prioritized(h.trigger("a::1::0", "ahead"), normal, 1)
	behind := prioritized(h.trigger("b::1::0", "behind"), normal, 1)
	require.True(t, q.enqueue(ahead, false))
	require.True(t, q.enqueue(behind, false))
	require.True(t, q.enqueue(h.trigger("a::2::0", "ahead"), false))
	require.True(t, q.enqueue(h.trigger("b::2::0", "behind"), false))

	lv := &q.levels[levelOf(ahead)]
	lv.names["ahead"].dispatchesPerWeight = rebaseAt - 1
	lv.names["behind"].dispatchesPerWeight = rebaseAt - 1 - oneDispatch
	lv.startAt = rebaseAt - 1

	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []string{"behind", "ahead", "behind", "ahead"}, []string{nameOf(order[0]), nameOf(order[1]), nameOf(order[2]), nameOf(order[3])})
	assert.Less(t, lv.startAt, uint64(rebaseAt), "counts were rebased")
	assert.Equal(t, uint64(2*oneDispatch), lv.startAt, "four dispatches at weight 1 from a rebased zero, two per name")
}

func TestQueue_ReturningNameEarnsNothing(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 200 {
		require.True(t, q.enqueue(prioritized(h.trigger("b::"+strconv.Itoa(i)+"::0", "backlog"), normal, 3), false))
	}

	// A name that empties and re-enters after every delivery still gets 1 in 4.
	backlogServed := 0
	for i := range 20 {
		next := prioritized(h.trigger("t::"+strconv.Itoa(i)+"::0", "trickle"), normal, 1)
		require.True(t, q.enqueue(next, false))
		for {
			var served *loops.TriggerRequest
			require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
				served = r
				return true
			}))
			if served == next {
				break
			}
			backlogServed++
		}
	}
	assert.InDelta(t, 60, backlogServed, 3, "3 backlog turns per trickle turn")
}

func TestQueue_HeavyReturningNameKeepsItsShare(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 400 {
		require.True(t, q.enqueue(h.trigger("l::"+strconv.Itoa(i)+"::0", "light"), false))
	}

	// A weight 3 name that always has exactly one trigger waiting, sent as
	// soon as the previous one was served, still gets 3 turns in 4.
	counts := map[string]int{}
	next := 0
	enqueueHeavy := func() {
		require.True(t, q.enqueue(prioritized(h.trigger("h::"+strconv.Itoa(next)+"::0", "heavy"), normal, 3), false))
		next++
	}
	enqueueHeavy()
	for range 400 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
		if q.levels[2].names["heavy"].n == 0 {
			enqueueHeavy()
		}
	}
	assert.InDelta(t, 300, counts["heavy"], 2)
	assert.InDelta(t, 100, counts["light"], 2)
}

func TestQueue_LongGoneNameStartsLikeNew(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	lv := &q.levels[2]
	for i := range 20 {
		require.True(t, q.enqueue(h.trigger("l::"+strconv.Itoa(i)+"::0", "light"), false))
	}
	require.True(t, q.enqueue(prioritized(h.trigger("h::0::0", "heavy"), normal, 3), false))

	// Serve heavy once; its queue is now empty but its count is kept. One
	// weight 1 dispatch is a full round, so one more light serve moves the
	// clock past it and it is forgotten.
	served := func() string {
		var name string
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool { name = nameOf(r); return true }))
		return name
	}
	for served() != "heavy" {
	}
	assert.Contains(t, lv.names, "heavy", "within a round the count is kept")
	assert.Zero(t, lv.names["heavy"].n)
	assert.Less(t, lv.names["heavy"].dispatchesPerWeight, lv.startAt, "and it is behind, owed its next turns")
	require.Equal(t, "light", served())
	assert.NotContains(t, lv.names, "heavy", "a round later it is forgotten")

	require.True(t, q.enqueue(prioritized(h.trigger("h::1::0", "heavy"), normal, 3), false))
	assert.Equal(t, lv.startAt, lv.names["heavy"].dispatchesPerWeight, "and comes back like a new name")
}

func TestQueue_SkippedNameIsCharged(t *testing.T) {
	t.Parallel()
	q := newQueue(2000)
	h := newHarness(t, 0, 0)
	for i := range 400 {
		for _, name := range []string{"x", "y", "z"} {
			require.True(t, q.enqueue(h.trigger(name+"::"+strconv.Itoa(i)+"::0", name), false))
		}
	}

	// x cannot go for 300 turns.
	counts := map[string]int{}
	for range 300 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			if nameOf(r) == "x" {
				return false
			}
			counts[nameOf(r)]++
			return true
		}))
	}
	require.Equal(t, 0, counts["x"])

	// Then it gets a third, not every turn until it has caught up.
	counts = map[string]int{}
	for range 300 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.InDelta(t, 100, counts["x"], 2)
	assert.InDelta(t, 100, counts["y"], 2)
	assert.InDelta(t, 100, counts["z"], 2)
}

func TestQueue_InstancesRotateWithinAName(t *testing.T) {
	t.Parallel()
	q := newQueue(10)
	h := newHarness(t, 0, 0)
	a1, a2, b1 := h.trigger("a::1::0", "act"), h.trigger("a::2::0", "act"), h.trigger("b::1::0", "act")
	for _, r := range []*loops.TriggerRequest{a1, a2, b1} {
		require.True(t, q.enqueue(r, false))
	}
	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []*loops.TriggerRequest{a1, b1, a2}, order, "round robin across instances, FIFO inside one")
}

func TestQueue_DrainAll(t *testing.T) {
	t.Parallel()
	q := newQueue(10)
	h := newHarness(t, 0, 0)
	n := h.trigger("wf::1::0", "act")
	c := prioritized(h.trigger("wf::2::0", "act"), critical, 1)
	require.True(t, q.enqueue(n, false))
	require.True(t, q.enqueue(c, false))
	var drained []*loops.TriggerRequest
	q.drainAll(func(r *loops.TriggerRequest) { drained = append(drained, r) })
	assert.Equal(t, []*loops.TriggerRequest{c, n}, drained)
	assert.Equal(t, 0, q.len())
	assert.False(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return true }))
}

func TestQueue_Bound(t *testing.T) {
	t.Parallel()
	q := newQueue(1)
	h := newHarness(t, 0, 0)
	require.True(t, q.enqueue(h.trigger("wf::1::0", "act"), false))
	assert.False(t, q.enqueue(h.trigger("wf::2::0", "act"), false), "a full queue refuses a new trigger")
	assert.True(t, q.enqueue(h.trigger("wf::3::0", "act"), true), "an admitted trigger parks past the bound")
	assert.Equal(t, 2, q.len())
}

func TestQueue_Remove(t *testing.T) {
	t.Parallel()
	q := newQueue(10)
	h := newHarness(t, 0, 0)
	n1, n2 := h.trigger("wf::1::0", "act"), h.trigger("wf::2::0", "act")
	c := prioritized(h.trigger("wf::3::0", "act"), critical, 1)
	for _, r := range []*loops.TriggerRequest{n1, n2, c} {
		require.True(t, q.enqueue(r, false))
	}
	assert.Nil(t, q.remove(key("missing::1::0")))
	assert.Same(t, n1, q.remove(key("wf::1::0")))
	assert.Equal(t, 2, q.len())
	assert.Same(t, c, q.remove(key("wf::3::0")))
	assert.Zero(t, q.levels[levelOf(c)].names["act"].n, "the emptied name keeps its count")
	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []*loops.TriggerRequest{n2}, order)
	assert.Equal(t, 0, q.len())
}
