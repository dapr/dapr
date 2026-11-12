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
	"slices"
	"strings"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// passPerDelivery is what a delivery costs a name at weight 1; weight w costs
// passPerDelivery / w.
const passPerDelivery = 1000000

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

// level is the names waiting at one priority level. vclock is where a name
// that starts or resumes waiting begins.
type level struct {
	names  map[string]*flow
	vclock uint64
}

// flow is the waiting triggers of one name at one level, grouped by workflow
// instance. The lowest pass is served next.
type flow struct {
	name   string
	seq    uint64
	pass   uint64
	groups map[string][]*loops.TriggerRequest
	ring   []string
	next   int
	n      int
}

func newQueue(maxLen int) *queue {
	return &queue{max: maxLen}
}

// levelOf is the index of a trigger's priority level, unset meaning normal.
func levelOf(req *loops.TriggerRequest) int {
	l := req.Job.GetMetadata().GetPriorityLevel()
	if l == schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED {
		l = schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL
	}
	return min(int(l)-1, numLevels-1)
}

// levelName is the metrics label of a level index.
func levelName(level int) string {
	return strings.ToLower(strings.TrimPrefix(schedulerv1pb.PriorityLevel(level+1).String(), "PRIORITY_LEVEL_"))
}

// weightOf is a trigger's fairness weight, unset meaning 1 and never so large
// that a delivery is free.
func weightOf(req *loops.TriggerRequest) uint64 {
	w := uint64(req.Job.GetMetadata().GetFairnessWeight())
	if w == 0 {
		return 1
	}
	return min(w, passPerDelivery)
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
		f = &flow{name: name, seq: q.seq, pass: lv.vclock, groups: make(map[string][]*loops.TriggerRequest)}
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
// accepted, and removes it. The accepted name is charged; the names offered
// before it at that level are brought up to the clock.
func (q *queue) tryDispatchOne(fn func(*loops.TriggerRequest) bool) bool {
	for i := range q.levels {
		lv := &q.levels[i]
		if len(lv.names) == 0 {
			continue
		}
		var skipped []*flow
		for _, f := range lv.byPass() {
			req, ok := f.offer(fn)
			if !ok {
				skipped = append(skipped, f)
				continue
			}
			f.pass += passPerDelivery / weightOf(req)
			lv.vclock = max(lv.vclock, f.pass)
			for _, s := range skipped {
				s.pass = max(s.pass, lv.vclock)
			}
			if f.n == 0 {
				delete(lv.names, f.name)
			}
			q.n--
			return true
		}
	}
	return false
}

// byPass returns the level's names in serving order.
func (lv *level) byPass() []*flow {
	fs := make([]*flow, 0, len(lv.names))
	for _, f := range lv.names {
		fs = append(fs, f)
	}
	slices.SortFunc(fs, func(a, b *flow) int {
		return cmp.Or(cmp.Compare(a.pass, b.pass), cmp.Compare(a.seq, b.seq))
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
		for name, f := range lv.names {
			req := f.remove(key)
			if req == nil {
				continue
			}
			if f.n == 0 {
				delete(lv.names, name)
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
		for _, f := range lv.byPass() {
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
