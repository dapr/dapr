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
// one dispatch at weight 1 counts oneDispatch, so that dividing by the weight
// stays a whole number. A level's counts are rebased once they reach
// rebaseAt, a billion billion, long before they could overflow.
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

// level is the names at one priority level. startAt is the highest count
// reached, where a new name begins. A name whose queue empties keeps its
// count for one round, so it resumes where it left off; after that it is
// forgotten and starts like a new name.
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
	groups              map[string][]*loops.TriggerRequest
	ring                []string
	next                int
	n                   int
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

// enqueue parks req. Past the bound only an admitted trigger, one a sidecar
// already refused, is parked.
func (q *queue) enqueue(req *loops.TriggerRequest, admitted bool) bool {
	if !admitted && q.n >= q.max {
		return false
	}
	meta := req.Job.GetMetadata()
	lv := &q.levels[levelOf(req)]
	name := meta.GetConcurrencyKey()
	f, ok := lv.names[name]
	if !ok {
		if lv.names == nil {
			lv.names = make(map[string]*flow)
		}
		q.seq++
		f = &flow{name: name, seq: q.seq, dispatchesPerWeight: lv.startAt, groups: make(map[string][]*loops.TriggerRequest)}
		lv.names[name] = f
	}
	inst := instanceKey(meta.GetTarget().GetActor().GetId())
	if _, ok := f.groups[inst]; !ok {
		f.ring = append(f.ring, inst)
	}
	f.groups[inst] = append(f.groups[inst], req)
	f.n++
	q.n++
	return true
}

// tryDispatchOne offers parked triggers to fn in serving order until one is
// accepted, and removes it. The accepted name is charged the dispatch; the
// names offered before it at that level are brought up to startAt.
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
			for _, s := range skipped {
				s.dispatchesPerWeight = max(s.dispatchesPerWeight, lv.startAt)
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

// forget drops the empty names that are a full round or more behind startAt.
func (lv *level) forget() {
	for name, f := range lv.names {
		if f.n == 0 && f.dispatchesPerWeight+oneDispatch <= lv.startAt {
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

// offer offers each instance's head in ring order, starting after the
// instance served last, until fn accepts one.
func (f *flow) offer(fn func(*loops.TriggerRequest) bool) (*loops.TriggerRequest, bool) {
	for i := range len(f.ring) {
		pos := (f.next + i) % len(f.ring)
		key := f.ring[pos]
		reqs := f.groups[key]
		req := reqs[0]
		if !fn(req) {
			continue
		}
		reqs[0] = nil
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
		lv := &q.levels[i]
		for _, f := range lv.names {
			req := f.remove(key)
			if req == nil {
				continue
			}
			q.n--
			return req
		}
	}
	return nil
}

func (f *flow) remove(key string) *loops.TriggerRequest {
	for pos, inst := range f.ring {
		reqs := f.groups[inst]
		i := slices.IndexFunc(reqs, func(req *loops.TriggerRequest) bool { return req.Job.GetKey() == key })
		if i < 0 {
			continue
		}
		req := reqs[i]
		if len(reqs) > 1 {
			f.groups[inst] = slices.Delete(reqs, i, i+1)
		} else {
			delete(f.groups, inst)
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
	return nil
}

// drainAll removes every parked trigger, passing each to fn in serving order.
func (q *queue) drainAll(fn func(*loops.TriggerRequest)) {
	for i := range q.levels {
		lv := &q.levels[i]
		for _, f := range lv.furthestBehind() {
			for _, inst := range f.ring {
				for _, req := range f.groups[inst] {
					fn(req)
				}
			}
		}
		lv.names = nil
	}
	q.n = 0
}
