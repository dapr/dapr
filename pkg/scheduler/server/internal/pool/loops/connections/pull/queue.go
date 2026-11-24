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
	"container/heap"
	"slices"
	"strings"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// One dispatch at weight 1 counts oneDispatch, and at weight w it counts
// oneDispatch / w, truncated, an error of at most 0.1%. A level's counts are
// rebased once they reach rebaseAt, a billion billion, long before they could
// overflow.
const (
	MinFairnessWeight = 1
	MaxFairnessWeight = 1000
	oneDispatch       = 1000000
	rebaseAt          = 1000000000000000000
)

// numLevels is the number of priority levels, critical through background.
const numLevels = 5

// queue parks pull triggers of one actor type while no sidecar has a free
// slot: by priority level, then by name in proportion to fairness weight, then
// round robin across workflow instances, then arrival order. Names share
// turns before instances rotate, also for triggers with neither field set: a
// name is one share however many instances it has.
type queue struct {
	max    int
	n      int
	seq    uint64
	levels [numLevels]level
}

// level is the names at one priority level. startAt is the highest count a
// name had when it was served. A new name starts there, a name that could not
// go is brought up to it, and an empty name is forgotten once it is not ahead
// of it, so a name that leaves and returns keeps only the turns it is owed.
// waiting holds the names with triggers, furthest behind first. idle holds the
// empty names not yet forgotten. evictable holds the arrival order of every
// trigger that can be evicted, and appended lists them oldest first, with
// entries that have since left removed lazily.
type level struct {
	names     map[string]*flow
	startAt   uint64
	waiting   waiting
	idle      map[string]*flow
	evictable map[uint64]struct{}
	appended  []slot
}

// slot locates a parked trigger that can be evicted.
type slot struct {
	f    *flow
	inst string
	seq  uint64
}

// flow is the triggers of one name at one level, grouped by workflow
// instance. The name with the lowest dispatches ÷ weight is furthest behind
// its share and is served next. weight is the weight of its newest trigger,
// so a name has one share even while triggers of an older weight are parked.
type flow struct {
	lv                  *level
	name                string
	seq                 uint64
	dispatchesPerWeight uint64
	weight              uint64
	index               int
	groups              map[string][]parked
	ring                []string
	next                int
	n                   int
}

// parked is a waiting trigger and its arrival order. An admitted one was
// refused by a sidecar and is never evicted.
type parked struct {
	req      *loops.TriggerRequest
	seq      uint64
	admitted bool
}

func newQueue(maxLen int) *queue {
	return &queue{max: maxLen}
}

// levelOf is the index of a trigger's priority level. Unset means normal, and
// so does a level this version does not know: ScheduleJob rejects those, but
// a job stored by a newer Scheduler can still carry one after a downgrade.
func levelOf(req *loops.TriggerRequest) int {
	l := req.Job.GetMetadata().GetPriority().GetLevel()
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

// weightOf is a trigger's fairness weight. Unset means 1, and so does a
// weight above 1000: ScheduleJob rejects those, but a job stored by a newer
// Scheduler can still carry one after a downgrade.
func weightOf(req *loops.TriggerRequest) uint64 {
	w := req.Job.GetMetadata().GetPriority().GetFairnessWeight()
	if w < MinFairnessWeight || w > MaxFairnessWeight {
		return 1
	}
	return uint64(w)
}

// instanceKey is the workflow instance ID in an activity actor ID
// "<instanceID>::<taskID>::<generation>". Any other actor ID is its own key.
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
// lowest level with a trigger that can be evicted, in which case the newest
// such trigger of that level is evicted and returned for the caller to
// resolve.
func (q *queue) enqueue(req *loops.TriggerRequest, admitted bool) (evicted *loops.TriggerRequest, ok bool) {
	li := levelOf(req)
	if !admitted && q.n >= q.max {
		lowest := q.lowestEvictable()
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
		f = &flow{lv: lv, name: name, seq: q.seq, dispatchesPerWeight: lv.startAt, index: -1, groups: make(map[string][]parked)}
		lv.names[name] = f
	}
	f.weight = weightOf(req)
	inst := instanceKey(meta.GetTarget().GetActor().GetId())
	if _, ok := f.groups[inst]; !ok {
		f.ring = append(f.ring, inst)
	}
	q.seq++
	f.groups[inst] = append(f.groups[inst], parked{req: req, seq: q.seq, admitted: admitted})
	if !admitted {
		if lv.evictable == nil {
			lv.evictable = make(map[uint64]struct{})
		}
		lv.evictable[q.seq] = struct{}{}
		lv.appended = append(lv.appended, slot{f: f, inst: inst, seq: q.seq})
	}
	if f.n == 0 {
		delete(lv.idle, name)
		heap.Push(&lv.waiting, f)
	}
	f.n++
	q.n++
	return evicted, true
}

// lowestEvictable is the index of the lowest level with a trigger that is not
// admitted, or -1 when none has.
func (q *queue) lowestEvictable() int {
	for i := numLevels - 1; i >= 0; i-- {
		if len(q.levels[i].evictable) > 0 {
			return i
		}
	}
	return -1
}

// tryDispatchOne offers parked triggers to fn in serving order until one is
// accepted, and removes it. The accepted name is charged. The names offered
// before it at that level are brought up to startAt.
func (q *queue) tryDispatchOne(fn func(*loops.TriggerRequest) bool) bool {
	for i := range q.levels {
		lv := &q.levels[i]
		var skipped []*flow
		for lv.waiting.Len() > 0 {
			f := heap.Pop(&lv.waiting).(*flow)
			if !f.offer(fn) {
				skipped = append(skipped, f)
				continue
			}
			lv.startAt = max(lv.startAt, f.dispatchesPerWeight)
			f.dispatchesPerWeight += oneDispatch / f.weight
			for _, s := range skipped {
				s.dispatchesPerWeight = max(s.dispatchesPerWeight, lv.startAt)
				heap.Push(&lv.waiting, s)
			}
			if f.n > 0 {
				heap.Push(&lv.waiting, f)
			}
			lv.forget()
			if lv.startAt >= rebaseAt {
				lv.rebase()
			}
			q.n--
			return true
		}
		for _, s := range skipped {
			heap.Push(&lv.waiting, s)
		}
	}
	return false
}

// forget drops the empty names that are not ahead of startAt.
func (lv *level) forget() {
	for name, f := range lv.idle {
		if f.dispatchesPerWeight <= lv.startAt {
			delete(lv.idle, name)
			delete(lv.names, name)
		}
	}
}

// rebase subtracts startAt from every name and resets it, which keeps their
// order and keeps the counts from overflowing.
func (lv *level) rebase() {
	for _, f := range lv.names {
		f.dispatchesPerWeight -= min(f.dispatchesPerWeight, lv.startAt)
	}
	lv.startAt = 0
	heap.Init(&lv.waiting)
}

// evictNewest removes and returns the trigger that arrived last at this
// level among those not admitted.
func (lv *level) evictNewest() *loops.TriggerRequest {
	for len(lv.appended) > 0 {
		s := lv.appended[len(lv.appended)-1]
		lv.appended = lv.appended[:len(lv.appended)-1]
		if _, ok := lv.evictable[s.seq]; !ok {
			continue
		}
		for i, p := range slices.Backward(s.f.groups[s.inst]) {
			if p.seq == s.seq {
				return s.f.take(s.inst, i)
			}
		}
	}
	return nil
}

// left records that p is no longer parked in f.
func (f *flow) left(p parked) {
	lv := f.lv
	if !p.admitted {
		delete(lv.evictable, p.seq)
		if len(lv.appended) > 2*len(lv.evictable)+64 {
			lv.appended = slices.DeleteFunc(lv.appended, func(s slot) bool {
				_, ok := lv.evictable[s.seq]
				return !ok
			})
		}
	}
	f.n--
	if f.n > 0 {
		return
	}
	if f.index >= 0 {
		heap.Remove(&lv.waiting, f.index)
	}
	if lv.idle == nil {
		lv.idle = make(map[string]*flow)
	}
	lv.idle[f.name] = f
}

// offer offers each instance's head in ring order, starting after the
// instance served last, until fn accepts one.
func (f *flow) offer(fn func(*loops.TriggerRequest) bool) bool {
	for i := range len(f.ring) {
		pos := (f.next + i) % len(f.ring)
		key := f.ring[pos]
		reqs := f.groups[key]
		head := reqs[0]
		if !fn(head.req) {
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
		f.left(head)
		return true
	}
	return false
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
	p := reqs[idx]
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
	f.left(p)
	return p.req
}

// drainAll removes every parked trigger, passing each to fn in serving order.
func (q *queue) drainAll(fn func(*loops.TriggerRequest)) {
	for i := range q.levels {
		lv := &q.levels[i]
		order := slices.Clone(lv.waiting)
		slices.SortFunc(order, func(a, b *flow) int {
			return cmp.Or(cmp.Compare(a.dispatchesPerWeight, b.dispatchesPerWeight), cmp.Compare(a.seq, b.seq))
		})
		for _, f := range order {
			for _, inst := range f.ring {
				for _, p := range f.groups[inst] {
					fn(p.req)
				}
			}
		}
		*lv = level{startAt: lv.startAt}
	}
	q.n = 0
}
