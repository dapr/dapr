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
func prioritized(req *loops.TriggerRequest, level schedulerv1pb.PriorityLevel, weight uint32) *loops.TriggerRequest {
	req.Job.Metadata.PriorityLevel = level.Enum()
	req.Job.Metadata.FairnessWeight = &weight
	return req
}

// mustEnqueue parks a trigger that must fit without evicting another.
func mustEnqueue(t *testing.T, q *queue, req *loops.TriggerRequest) {
	t.Helper()
	evicted, ok := q.enqueue(req, false)
	require.True(t, ok)
	require.Nil(t, evicted)
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
	assert.Equal(t, uint64(1), weightOf(unset), "unset is weight 1")
	assert.Equal(t, uint64(2), weightOf(prioritized(h.trigger("wf::11::0", "act"), normal, 2)))
	assert.Equal(t, uint64(500), weightOf(prioritized(h.trigger("wf::12::0", "act"), normal, 500)))
	assert.Equal(t, uint64(1000), weightOf(prioritized(h.trigger("wf::13::0", "act"), normal, 1000)))
	assert.Equal(t, uint64(1), weightOf(prioritized(h.trigger("wf::14::0", "act"), normal, 1001)), "above the range counts as unset")
	assert.Equal(t, 0, levelOf(prioritized(h.trigger("wf::2::0", "act"), critical, 3)))
	assert.Equal(t, 1, levelOf(prioritized(h.trigger("wf::7::0", "act"), high, 1)))
	assert.Equal(t, 2, levelOf(prioritized(h.trigger("wf::8::0", "act"), normal, 1)))
	assert.Equal(t, 3, levelOf(prioritized(h.trigger("wf::9::0", "act"), low, 1)))
	assert.Equal(t, 4, levelOf(prioritized(h.trigger("wf::3::0", "act"), background, 0)))
	assert.Equal(t, uint64(1), weightOf(prioritized(h.trigger("wf::4::0", "act"), normal, 0)), "0 is weight 1")
	assert.Equal(t, uint64(1), weightOf(prioritized(h.trigger("wf::6::0", "act"), normal, 2000)), "above the range counts as unset")
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
		mustEnqueue(t, q, r)
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
	mustEnqueue(t, q, a1)
	mustEnqueue(t, q, b1)
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
		mustEnqueue(t, q, r)
	}

	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []*loops.TriggerRequest{c, hi, n, lo, bg}, order, "arrival order does not matter across levels, the same name at two levels is two names")

	// A critical head that cannot go does not hold a lower level back.
	mustEnqueue(t, q, c)
	mustEnqueue(t, q, n)
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
		mustEnqueue(t, q, prioritized(h.trigger("h::"+strconv.Itoa(i)+"::0", "heavy"), normal, 3))
		mustEnqueue(t, q, prioritized(h.trigger("l::"+strconv.Itoa(i)+"::0", "light"), normal, 1))
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
		mustEnqueue(t, q, prioritized(h.trigger("a::"+strconv.Itoa(i)+"::0", "six"), normal, 600))
		mustEnqueue(t, q, prioritized(h.trigger("b::"+strconv.Itoa(i)+"::0", "four"), normal, 400))
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

func TestQueue_WeightAgainstUnset(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 300 {
		mustEnqueue(t, q, prioritized(h.trigger("a::"+strconv.Itoa(i)+"::0", "two"), normal, 2))
		mustEnqueue(t, q, h.trigger("b::"+strconv.Itoa(i)+"::0", "unset"))
	}

	counts := map[string]int{}
	for range 300 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.InDelta(t, 200, counts["two"], 1, "weight 2 against the default 1 is 2 turns in 3")
	assert.InDelta(t, 100, counts["unset"], 1)
}

func TestLevel_Rebase(t *testing.T) {
	t.Parallel()

	// Exact arithmetic: every count drops by startAt, so the gaps between
	// names are unchanged, and startAt is zero.
	lv := &level{names: map[string]*flow{
		"a": {name: "a", dispatchesPerWeight: 5000},
		"b": {name: "b", dispatchesPerWeight: 7500},
		"c": {name: "c", dispatchesPerWeight: 9000},
	}, startAt: 5000}
	lv.rebase()
	assert.Equal(t, uint64(0), lv.names["a"].dispatchesPerWeight)
	assert.Equal(t, uint64(2500), lv.names["b"].dispatchesPerWeight)
	assert.Equal(t, uint64(4000), lv.names["c"].dispatchesPerWeight)
	assert.Equal(t, uint64(0), lv.startAt)

	// A single name drops to zero, and so does startAt.
	lv = &level{names: map[string]*flow{"only": {name: "only", dispatchesPerWeight: 123456}}, startAt: 123456}
	lv.rebase()
	assert.Equal(t, uint64(0), lv.names["only"].dispatchesPerWeight)
	assert.Equal(t, uint64(0), lv.startAt)

	// With no names only startAt is left, and it drops to zero.
	lv = &level{startAt: 42}
	lv.rebase()
	assert.Equal(t, uint64(0), lv.startAt)

	// A name behind startAt stops at zero rather than wrapping.
	lv = &level{names: map[string]*flow{"behind": {name: "behind", dispatchesPerWeight: 5}}, startAt: 10}
	lv.rebase()
	assert.Equal(t, uint64(0), lv.names["behind"].dispatchesPerWeight)

	// Rebasing twice is a no-op the second time.
	lv = &level{names: map[string]*flow{"a": {name: "a", dispatchesPerWeight: 10}, "b": {name: "b", dispatchesPerWeight: 30}}, startAt: 10}
	lv.rebase()
	lv.rebase()
	assert.Equal(t, uint64(0), lv.names["a"].dispatchesPerWeight)
	assert.Equal(t, uint64(20), lv.names["b"].dispatchesPerWeight)
	assert.Equal(t, uint64(0), lv.startAt)
}

func TestQueue_RebaseNotBeforeThreshold(t *testing.T) {
	t.Parallel()
	q := newQueue(100)
	h := newHarness(t, 0, 0)
	mustEnqueue(t, q, h.trigger("a::1::0", "a"))
	mustEnqueue(t, q, h.trigger("a::2::0", "a"))
	lv := &q.levels[levelOf(h.trigger("x::1::0", "a"))]
	lv.names["a"].dispatchesPerWeight = rebaseAt - oneDispatch
	lv.startAt = rebaseAt - oneDispatch

	require.True(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return true }))
	assert.Equal(t, uint64(rebaseAt-oneDispatch), lv.startAt, "one dispatch short of the threshold, no rebase")
	assert.Equal(t, uint64(rebaseAt), lv.names["a"].dispatchesPerWeight)
	require.True(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return true }))
	assert.Equal(t, uint64(0), lv.startAt, "the dispatch that reaches the threshold rebases")
	assert.Equal(t, uint64(oneDispatch), lv.names["a"].dispatchesPerWeight, "the lone name keeps its one charge past startAt")
}

func TestQueue_RebaseThenReturningNameStartsLow(t *testing.T) {
	t.Parallel()
	q := newQueue(100)
	h := newHarness(t, 0, 0)
	for i := range 3 {
		mustEnqueue(t, q, h.trigger("s::"+strconv.Itoa(i)+"::0", "stayer"))
	}
	lv := &q.levels[levelOf(h.trigger("x::1::0", "stayer"))]
	lv.names["stayer"].dispatchesPerWeight = rebaseAt
	lv.startAt = rebaseAt - 1

	// The first dispatch crosses the threshold and rebases.
	require.True(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return true }))
	require.Equal(t, uint64(0), lv.startAt)
	require.Equal(t, uint64(oneDispatch), lv.names["stayer"].dispatchesPerWeight)

	// A name that arrives now starts at the rebased startAt, not the old
	// huge value, so it is served in turn with the stayer rather than
	// starving it or being starved.
	mustEnqueue(t, q, h.trigger("n::1::0", "newcomer"))
	assert.Equal(t, uint64(0), lv.names["newcomer"].dispatchesPerWeight)
	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []string{"newcomer", "stayer", "stayer"}, []string{nameOf(order[0]), nameOf(order[1]), nameOf(order[2])})
}

func TestQueue_RebaseKeepsOrder(t *testing.T) {
	t.Parallel()
	q := newQueue(100)
	h := newHarness(t, 0, 0)
	ahead := prioritized(h.trigger("a::1::0", "ahead"), normal, 1)
	behind := prioritized(h.trigger("b::1::0", "behind"), normal, 1)
	mustEnqueue(t, q, ahead)
	mustEnqueue(t, q, behind)
	mustEnqueue(t, q, h.trigger("a::2::0", "ahead"))
	mustEnqueue(t, q, h.trigger("b::2::0", "behind"))

	lv := &q.levels[levelOf(ahead)]
	lv.names["ahead"].dispatchesPerWeight = rebaseAt - 1
	lv.names["behind"].dispatchesPerWeight = rebaseAt - 1 - oneDispatch
	lv.startAt = rebaseAt - 1 - oneDispatch

	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []string{"behind", "ahead", "behind", "ahead"}, []string{nameOf(order[0]), nameOf(order[1]), nameOf(order[2]), nameOf(order[3])})
	assert.Equal(t, uint64(0), lv.startAt, "counts were rebased on the fourth dispatch")
	assert.Equal(t, uint64(oneDispatch), lv.names["ahead"].dispatchesPerWeight, "and ahead, served last, is still one charge past startAt")
	assert.NotContains(t, lv.names, "behind", "behind emptied at startAt and was forgotten")
}

func TestQueue_ReturningNameEarnsNothing(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 200 {
		mustEnqueue(t, q, prioritized(h.trigger("b::"+strconv.Itoa(i)+"::0", "backlog"), normal, 3))
	}

	// A name that empties and re-enters after every delivery still gets 1 in 4.
	backlogServed := 0
	for i := range 20 {
		next := prioritized(h.trigger("t::"+strconv.Itoa(i)+"::0", "trickle"), normal, 1)
		mustEnqueue(t, q, next)
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
		mustEnqueue(t, q, h.trigger("l::"+strconv.Itoa(i)+"::0", "light"))
	}

	// A weight 3 name that always has exactly one trigger waiting, sent as
	// soon as the previous one was served, still gets 3 turns in 4.
	counts := map[string]int{}
	next := 0
	enqueueHeavy := func() {
		mustEnqueue(t, q, prioritized(h.trigger("h::"+strconv.Itoa(next)+"::0", "heavy"), normal, 3))
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
		mustEnqueue(t, q, h.trigger("l::"+strconv.Itoa(i)+"::0", "light"))
	}
	mustEnqueue(t, q, prioritized(h.trigger("h::0::0", "heavy"), normal, 3))

	// Serve heavy once; its queue is now empty but its count is kept while it
	// is ahead of startAt. The next light serve starts from a count past it,
	// so it is forgotten.
	served := func() string {
		var name string
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool { name = nameOf(r); return true }))
		return name
	}
	for served() != "heavy" {
	}
	assert.Contains(t, lv.names, "heavy", "the count is kept")
	assert.Zero(t, lv.names["heavy"].n)
	assert.Less(t, lv.names["heavy"].dispatchesPerWeight, lv.names["light"].dispatchesPerWeight, "and it is behind, owed its next turns")
	require.Equal(t, "light", served())
	assert.NotContains(t, lv.names, "heavy", "once startAt passes it, it is forgotten")

	mustEnqueue(t, q, prioritized(h.trigger("h::1::0", "heavy"), normal, 3))
	assert.Equal(t, lv.startAt, lv.names["heavy"].dispatchesPerWeight, "and comes back like a new name")
	assert.Less(t, lv.names["heavy"].dispatchesPerWeight, lv.names["light"].dispatchesPerWeight)
}

func TestQueue_SkippedNameIsCharged(t *testing.T) {
	t.Parallel()
	q := newQueue(2000)
	h := newHarness(t, 0, 0)
	for i := range 400 {
		for _, name := range []string{"x", "y", "z"} {
			mustEnqueue(t, q, h.trigger(name+"::"+strconv.Itoa(i)+"::0", name))
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
		mustEnqueue(t, q, r)
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
	mustEnqueue(t, q, n)
	mustEnqueue(t, q, c)
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
	mustEnqueue(t, q, h.trigger("wf::1::0", "act"))
	_, ok := q.enqueue(h.trigger("wf::2::0", "act"), false)
	assert.False(t, ok, "a full queue refuses a new trigger of the same level")
	evicted, ok := q.enqueue(h.trigger("wf::3::0", "act"), true)
	assert.True(t, ok, "an admitted trigger parks past the bound")
	assert.Nil(t, evicted)
	assert.Equal(t, 2, q.len())
}

func TestQueue_HigherLevelEvictsNewestOfLowest(t *testing.T) {
	t.Parallel()
	q := newQueue(3)
	h := newHarness(t, 0, 0)
	bg1 := prioritized(h.trigger("b::1::0", "bg"), background, 1)
	bg2 := prioritized(h.trigger("b::2::0", "bg"), background, 1)
	lo := prioritized(h.trigger("l::1::0", "lo"), low, 1)
	for _, r := range []*loops.TriggerRequest{bg1, lo, bg2} {
		mustEnqueue(t, q, r)
	}

	// Full of low and background: a critical trigger evicts the newest
	// background one, not the low one and not the oldest.
	c := prioritized(h.trigger("c::1::0", "c"), critical, 1)
	evicted, ok := q.enqueue(c, false)
	require.True(t, ok)
	assert.Same(t, bg2, evicted)
	assert.Equal(t, 3, q.len())

	// A background trigger is refused while background is the lowest waiting.
	_, ok = q.enqueue(prioritized(h.trigger("b::3::0", "bg"), background, 1), false)
	assert.False(t, ok)

	// Once no background is left, low is the lowest and a normal one evicts it.
	evicted, ok = q.enqueue(prioritized(h.trigger("n::1::0", "n"), normal, 1), false)
	require.True(t, ok)
	assert.Same(t, bg1, evicted)
	evicted, ok = q.enqueue(prioritized(h.trigger("n::2::0", "n"), normal, 1), false)
	require.True(t, ok)
	assert.Same(t, lo, evicted)
	_, ok = q.enqueue(prioritized(h.trigger("n::3::0", "n"), normal, 1), false)
	assert.False(t, ok, "normal cannot evict normal")

	var order []*loops.TriggerRequest
	for q.tryDispatchOne(accept(&order)) {
	}
	assert.Equal(t, []string{"c", "n", "n"}, []string{nameOf(order[0]), nameOf(order[1]), nameOf(order[2])})
	assert.Equal(t, 0, q.len())
}

func TestQueue_NewNameStartsAtStartAt(t *testing.T) {
	t.Parallel()
	q := newQueue(100000)
	h := newHarness(t, 0, 0)
	for i := range 5000 {
		mustEnqueue(t, q, prioritized(h.trigger("a::"+strconv.Itoa(i)+"::0", "A"), normal, 1000))
	}
	mustEnqueue(t, q, prioritized(h.trigger("l::0::0", "L"), normal, 1))

	// Serve A, then the one L trigger, which moves the highest count a
	// thousand rounds ahead of A.
	served := func() string {
		var name string
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool { name = nameOf(r); return true }))
		return name
	}
	require.Equal(t, "A", served())
	require.Equal(t, "L", served())

	// A new weight 1000 name starts level with A, not with L, so the two
	// alternate from the start.
	for i := range 3000 {
		mustEnqueue(t, q, prioritized(h.trigger("c::"+strconv.Itoa(i)+"::0", "C"), normal, 1000))
	}
	counts := map[string]int{}
	for range 200 {
		counts[served()]++
	}
	assert.InDelta(t, 100, counts["A"], 1)
	assert.InDelta(t, 100, counts["C"], 1)
}

func TestQueue_BlockedHeavyNameKeepsItsShare(t *testing.T) {
	t.Parallel()
	q := newQueue(2000)
	h := newHarness(t, 0, 0)
	for i := range 500 {
		mustEnqueue(t, q, prioritized(h.trigger("h::"+strconv.Itoa(i)+"::0", "heavy"), normal, 3))
		mustEnqueue(t, q, h.trigger("l::"+strconv.Itoa(i)+"::0", "light"))
	}

	// Heavy's own gate is full on every 4th dispatch. Those are the only
	// turns light has to get, so heavy still gets 3 in 4.
	counts := map[string]int{}
	for i := range 400 {
		blocked := i%4 == 3
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			if blocked && nameOf(r) == "heavy" {
				return false
			}
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.InDelta(t, 300, counts["heavy"], 3)
	assert.InDelta(t, 100, counts["light"], 3)
}

func TestQueue_ReturningNameAgainstLighterBacklog(t *testing.T) {
	t.Parallel()
	for weight, want := range map[uint32]int{2: 667, 4: 800, 1000: 999} {
		q := newQueue(2000)
		h := newHarness(t, 0, 0)
		for i := range 1000 {
			mustEnqueue(t, q, h.trigger("b::"+strconv.Itoa(i)+"::0", "bulk"))
		}

		// A name with one trigger at a time, re-sent as soon as it is served,
		// against a weight 1 backlog gets its weighted share of 1000.
		counts := map[string]int{}
		next := 0
		enqueueSteady := func() {
			mustEnqueue(t, q, prioritized(h.trigger("s::"+strconv.Itoa(next)+"::0", "steady"), normal, weight))
			next++
		}
		enqueueSteady()
		for range 1000 {
			require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
				counts[nameOf(r)]++
				return true
			}))
			if q.levels[2].names["steady"].n == 0 {
				enqueueSteady()
			}
		}
		assert.InDelta(t, want, counts["steady"], 2, "weight %d", weight)
	}
}

func TestQueue_OneSkipCostsOneTurn(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 100 {
		mustEnqueue(t, q, prioritized(h.trigger("a::"+strconv.Itoa(i)+"::0", "A"), normal, 1000))
		mustEnqueue(t, q, prioritized(h.trigger("b::"+strconv.Itoa(i)+"::0", "B"), normal, 1000))
	}
	mustEnqueue(t, q, prioritized(h.trigger("l::0::0", "L"), normal, 1))

	// Serve until the one L trigger, a weight 1 charge, has gone.
	served := func(refuse string) string {
		var name string
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			if nameOf(r) == refuse {
				return false
			}
			name = nameOf(r)
			return true
		}))
		return name
	}
	for served("") != "L" {
	}

	// A's gate is full for one dispatch, so B goes. A is next, not a
	// thousand B turns later.
	require.Equal(t, "B", served("A"))
	assert.Equal(t, "A", served(""))
}

func TestQueue_RemovedNameDoesNotStarveANewOne(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	a0, a1 := prioritized(h.trigger("a::0::0", "A"), normal, 1000), prioritized(h.trigger("a::1::0", "A"), normal, 1000)
	mustEnqueue(t, q, a0)
	mustEnqueue(t, q, a1)
	mustEnqueue(t, q, prioritized(h.trigger("l::0::0", "L"), normal, 1))

	// A then L are served, then A's other trigger is removed rather than
	// served, which leaves A empty with its count.
	var order []*loops.TriggerRequest
	require.True(t, q.tryDispatchOne(accept(&order)))
	require.True(t, q.tryDispatchOne(accept(&order)))
	require.Equal(t, []string{"A", "L"}, []string{nameOf(order[0]), nameOf(order[1])})
	require.Same(t, a1, q.remove(key("a::1::0")))

	// A new name starts at startAt, which L's one weight 1 charge did not
	// move, so when A returns the two take turns.
	for i := range 10 {
		mustEnqueue(t, q, prioritized(h.trigger("n::"+strconv.Itoa(i)+"::0", "N"), normal, 1000))
		mustEnqueue(t, q, prioritized(h.trigger("a::"+strconv.Itoa(i+2)+"::0", "A"), normal, 1000))
	}
	counts := map[string]int{}
	for range 10 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.Equal(t, map[string]int{"N": 5, "A": 5}, counts)
}

func TestQueue_AdmittedIsNeverEvicted(t *testing.T) {
	t.Parallel()
	q := newQueue(2)
	h := newHarness(t, 0, 0)
	bg1 := prioritized(h.trigger("b::1::0", "bg"), background, 1)
	bg2 := prioritized(h.trigger("b::2::0", "bg"), background, 1)
	mustEnqueue(t, q, bg1)
	mustEnqueue(t, q, bg2)
	refused := prioritized(h.trigger("b::3::0", "bg"), background, 1)
	evicted, ok := q.enqueue(refused, true)
	require.True(t, ok)
	require.Nil(t, evicted)

	// The admitted trigger is the newest at its level but is passed over.
	evicted, ok = q.enqueue(prioritized(h.trigger("c::1::0", "c"), critical, 1), false)
	require.True(t, ok)
	assert.Same(t, bg2, evicted)
	evicted, ok = q.enqueue(prioritized(h.trigger("c::2::0", "c"), critical, 1), false)
	require.True(t, ok)
	assert.Same(t, bg1, evicted)

	// With only admitted triggers left below, a higher one is refused.
	_, ok = q.enqueue(prioritized(h.trigger("c::3::0", "c"), critical, 1), false)
	assert.False(t, ok)
	assert.Same(t, refused, q.remove(key("b::3::0")))
}

func TestQueue_NamesShareBeforeInstances(t *testing.T) {
	t.Parallel()
	q := newQueue(1000)
	h := newHarness(t, 0, 0)
	for i := range 100 {
		mustEnqueue(t, q, h.trigger("wf-"+strconv.Itoa(i)+"::0::0", "A"))
		mustEnqueue(t, q, h.trigger("one::"+strconv.Itoa(i)+"::0", "B"))
	}

	// 100 instances with one trigger of A and one instance with 100 of B,
	// neither field set: each name is one share, so B is not 1 in 101.
	counts := map[string]int{}
	for range 100 {
		require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
			counts[nameOf(r)]++
			return true
		}))
	}
	assert.Equal(t, map[string]int{"A": 50, "B": 50}, counts)
}

func TestQueue_Remove(t *testing.T) {
	t.Parallel()
	q := newQueue(10)
	h := newHarness(t, 0, 0)
	n1, n2 := h.trigger("wf::1::0", "act"), h.trigger("wf::2::0", "act")
	c := prioritized(h.trigger("wf::3::0", "act"), critical, 1)
	for _, r := range []*loops.TriggerRequest{n1, n2, c} {
		mustEnqueue(t, q, r)
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
