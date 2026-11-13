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
	"cmp"
	"math"
	"slices"
	"strings"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// Weights are kept in thousandths so that 0.5 and 1000 are whole numbers, and
// one dispatch at weight 1 counts oneDispatch. The division by the weight is
// truncated, an error of at most 0.1%. The range 0.001 to 1000 is Temporal's,
// so a weight carries over unchanged. A level's counts are rebased once they
// reach rebaseAt, a billion billion, long before they could overflow.
const (
	MinFairnessWeight = 0.001
	MaxFairnessWeight = 1000
	weightScale       = 1000
	oneDispatch       = 1000000
	rebaseAt          = 1000000000000000000
)

// numLevels is the number of priority levels, critical through background.
const numLevels = 5

// queue parks pull triggers of one actor type while no sidecar has a free
// slot: by priority level, then by name in proportion to fairness weight, then
// round robin across workflow instances, then arrival order.
type queue struct {
	max    int
	n      int
	seq    uint64
	levels [numLevels]level
}

// level is the names at one priority level. A new name starts at the lowest
// count among the names with triggers waiting. A name whose queue empties
// keeps its count for one round, so it resumes where it left off; after that
// it is forgotten. startAt is the highest count reached; a name that could
// not go is brought up to one round behind it, so it cannot bank more credit
// than that.
type level struct {
	names   map[string]*flow
	startAt uint64
}

// flow is the triggers of one name at one level, grouped by workflow
// instance. The name with the lowest dispatches ÷ weight is furthest behind
// its share and is served next.
type flow struct {
	name                string
	seq                 uint64
	dispatchesPerWeight uint64
	groups              map[string][]parked
	ring                []string
	next                int
	n                   int
}

// parked is a waiting trigger and its arrival order.
type parked struct {
	req *loops.TriggerRequest
	seq uint64
}

func newQueue(maxLen int) *queue {
	return &queue{max: maxLen}
}

// levelOf is the index of a trigger's priority level. Unset means normal, and
// so does a level this version does not know: ScheduleJob rejects those, but
// a job stored by a newer Scheduler can still carry one after a downgrade.
func levelOf(req *loops.TriggerRequest) int {
	l := req.Job.GetMetadata().GetPriorityLevel()
	if l < schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL || l > schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_BACKGROUND {
		l = schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL
	}
	return int(l) - 1
}

// levelNames are the metrics labels of the level indexes.
var levelNames = [numLevels]string{"critical", "high", "normal", "low", "background"}

func levelName(level int) string {
	return levelNames[level]
}

// weightOf is a trigger's fairness weight in thousandths. Unset means 1, and
// so does a weight outside 0.001 to 1000: ScheduleJob rejects those, but a
// job stored by a newer Scheduler can still carry one after a downgrade.
func weightOf(req *loops.TriggerRequest) uint64 {
	w := float64(req.Job.GetMetadata().GetFairnessWeight())
	if w == 0 || math.IsNaN(w) || w < MinFairnessWeight || w > MaxFairnessWeight {
		return weightScale
	}
	return uint64(math.Round(w * weightScale))
}

// instanceKey is the workflow instance ID in an activity actor ID
// "<instanceID>::<taskID>::<generation>"; any other actor ID is its own key.
func instanceKey(actorID string) string {
	if i := strings.LastIndex(actorID, "::"); i >= 0 {
		if j := strings.LastIndex(actorID[:i], "::"); j >= 0 {
			return actorID[:j]
		}
	}
	return actorID
}

func (q *queue) len() int {
	return q.n
}

// enqueue parks req. Past the bound a trigger is refused, unless it is
// admitted (a sidecar already accepted and refused it) or it outranks the
// lowest level with triggers waiting, in which case the newest trigger of
// that level is evicted and returned for the caller to resolve.
func (q *queue) enqueue(req *loops.TriggerRequest, admitted bool) (evicted *loops.TriggerRequest, ok bool) {
	li := levelOf(req)
	if !admitted && q.n >= q.max {
		lowest := q.lowestWaiting()
		if lowest <= li {
			return nil, false
		}
		evicted = q.levels[lowest].evictNewest()
		q.n--
	}

	meta := req.Job.GetMetadata()
	lv := &q.levels[li]
	name := meta.GetConcurrencyKey()
	f, ok := lv.names[name]
	if !ok {
		if lv.names == nil {
			lv.names = make(map[string]*flow)
		}
		q.seq++
		f = &flow{name: name, seq: q.seq, dispatchesPerWeight: lv.floor(), groups: make(map[string][]parked)}
		lv.names[name] = f
	}
	inst := instanceKey(meta.GetTarget().GetActor().GetId())
	if _, ok := f.groups[inst]; !ok {
		f.ring = append(f.ring, inst)
	}
	q.seq++
	f.groups[inst] = append(f.groups[inst], parked{req: req, seq: q.seq})
	f.n++
	q.n++
	return evicted, true
}

// lowestWaiting is the index of the lowest level with a trigger waiting, or
// -1 when none has.
func (q *queue) lowestWaiting() int {
	for i := numLevels - 1; i >= 0; i-- {
		for _, f := range q.levels[i].names {
			if f.n > 0 {
				return i
			}
		}
	}
	return -1
}

// tryDispatchOne offers parked triggers to fn in serving order until one is
// accepted, and removes it. The accepted name is charged; the names offered
// before it at that level are brought up to one round behind startAt.
func (q *queue) tryDispatchOne(fn func(*loops.TriggerRequest) bool) bool {
	for i := range q.levels {
		lv := &q.levels[i]
		if len(lv.names) == 0 {
			continue
		}
		var skipped []*flow
		for _, f := range lv.furthestBehind() {
			req, ok := f.offer(fn)
			if !ok {
				skipped = append(skipped, f)
				continue
			}
			f.dispatchesPerWeight += oneDispatch * weightScale / weightOf(req)
			lv.startAt = max(lv.startAt, f.dispatchesPerWeight)
			allowance := lv.startAt - min(lv.startAt, oneDispatch)
			for _, s := range skipped {
				s.dispatchesPerWeight = max(s.dispatchesPerWeight, allowance)
			}
			lv.forget()
			if lv.startAt >= rebaseAt {
				lv.rebase()
			}
			q.n--
			return true
		}
	}
	return false
}

// floor is the lowest count among the names with triggers waiting, or
// startAt when none has any.
func (lv *level) floor() uint64 {
	low, waiting := lv.startAt, false
	for _, f := range lv.names {
		if f.n > 0 && (!waiting || f.dispatchesPerWeight < low) {
			low, waiting = f.dispatchesPerWeight, true
		}
	}
	return low
}

// forget drops the empty names that are a full round or more behind the
// names with triggers waiting.
func (lv *level) forget() {
	floor := lv.floor()
	for name, f := range lv.names {
		if f.n == 0 && f.dispatchesPerWeight+oneDispatch <= floor {
			delete(lv.names, name)
		}
	}
}

// rebase subtracts the lowest count from every name and from startAt, which
// keeps their order and keeps the counts from overflowing.
func (lv *level) rebase() {
	low := lv.startAt
	for _, f := range lv.names {
		low = min(low, f.dispatchesPerWeight)
	}
	for _, f := range lv.names {
		f.dispatchesPerWeight -= low
	}
	lv.startAt -= low
}

// furthestBehind returns the level's names with triggers waiting, in serving
// order.
func (lv *level) furthestBehind() []*flow {
	fs := make([]*flow, 0, len(lv.names))
	for _, f := range lv.names {
		if f.n > 0 {
			fs = append(fs, f)
		}
	}
	slices.SortFunc(fs, func(a, b *flow) int {
		return cmp.Or(cmp.Compare(a.dispatchesPerWeight, b.dispatchesPerWeight), cmp.Compare(a.seq, b.seq))
	})
	return fs
}

// evictNewest removes and returns the trigger that arrived last at this
// level.
func (lv *level) evictNewest() *loops.TriggerRequest {
	var (
		newest *flow
		inst   string
		idx    int
		seq    uint64
	)
	for _, f := range lv.names {
		for key, reqs := range f.groups {
			if last := len(reqs) - 1; reqs[last].seq > seq {
				newest, inst, idx, seq = f, key, last, reqs[last].seq
			}
		}
	}
	return newest.take(inst, idx)
}

// offer offers each instance's head in ring order, starting after the
// instance served last, until fn accepts one.
func (f *flow) offer(fn func(*loops.TriggerRequest) bool) (*loops.TriggerRequest, bool) {
	for i := range len(f.ring) {
		pos := (f.next + i) % len(f.ring)
		key := f.ring[pos]
		reqs := f.groups[key]
		req := reqs[0].req
		if !fn(req) {
			continue
		}
		reqs[0] = parked{}
		if len(reqs) == 1 {
			delete(f.groups, key)
			f.ring = slices.Delete(f.ring, pos, pos+1)
			if len(f.ring) == 0 {
				f.next = 0
			} else {
				f.next = pos % len(f.ring)
			}
		} else {
			f.groups[key] = reqs[1:]
			f.next = (pos + 1) % len(f.ring)
		}
		f.n--
		return req, true
	}
	return nil, false
}

// remove takes the parked trigger of the job stored under key out of the
// queue, or returns nil.
func (q *queue) remove(key string) *loops.TriggerRequest {
	for i := range q.levels {
		for _, f := range q.levels[i].names {
			for inst, reqs := range f.groups {
				idx := slices.IndexFunc(reqs, func(p parked) bool { return p.req.Job.GetKey() == key })
				if idx < 0 {
					continue
				}
				q.n--
				return f.take(inst, idx)
			}
		}
	}
	return nil
}

// take removes the trigger at idx of instance inst and returns it.
func (f *flow) take(inst string, idx int) *loops.TriggerRequest {
	reqs := f.groups[inst]
	req := reqs[idx].req
	if len(reqs) > 1 {
		f.groups[inst] = slices.Delete(reqs, idx, idx+1)
	} else {
		delete(f.groups, inst)
		pos := slices.Index(f.ring, inst)
		f.ring = slices.Delete(f.ring, pos, pos+1)
		if f.next > pos {
			f.next--
		}
		if f.next >= len(f.ring) {
			f.next = 0
		}
	}
	f.n--
	return req
}

// drainAll removes every parked trigger, passing each to fn in serving order.
func (q *queue) drainAll(fn func(*loops.TriggerRequest)) {
	for i := range q.levels {
		lv := &q.levels[i]
		for _, f := range lv.furthestBehind() {
			for _, inst := range f.ring {
				for _, p := range f.groups[inst] {
					fn(p.req)
				}
			}
		}
		lv.names = nil
	}
	q.n = 0
}
