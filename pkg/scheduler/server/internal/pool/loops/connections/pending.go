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
	"slices"
	"time"

	"github.com/diagridio/go-etcd-cron/api"

	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// stride1 scales the stride arithmetic so that dividing by the largest
// weight still leaves a useful stride.
const stride1 = 1 << 20

// Pending triggers wait in a tree with three levels below the namespace
// root: the app, the actor type, and the flow (the trigger's concurrency
// key). Every node is served in proportion to its weight with stride
// scheduling, so apps share the namespace by app niceness and flows share
// their app by flow niceness. Within a flow, triggers are served in arrival
// order. The tree is not capped: the cron has at most one trigger of a job
// outstanding, so it never holds more than the jobs that are due.
const (
	depthApp = iota + 1
	depthActorType
	depthFlow
)

type pendingNode struct {
	key string

	// seq is the activation order of the node, breaking ties in pass so that
	// the flow which started waiting first is served first.
	seq uint64

	// pass is the virtual time the node has consumed; vclock is the pass of
	// the child last served, at which a newly waiting child starts so that a
	// flow cannot bank credit while it has nothing pending.
	pass   uint64
	vclock uint64

	size     int
	children map[string]*pendingNode
	items    []pendingItem
}

type pendingItem struct {
	req     *loops.TriggerRequest
	arrived time.Time
}

func (n *pendingNode) child(key string, seq uint64) *pendingNode {
	if n.children == nil {
		n.children = make(map[string]*pendingNode)
	}
	c, ok := n.children[key]
	if !ok {
		c = &pendingNode{key: key, seq: seq, pass: n.vclock}
		n.children[key] = c
	}
	return c
}

// byPass returns the children in serving order.
func (n *pendingNode) byPass() []*pendingNode {
	cs := make([]*pendingNode, 0, len(n.children))
	for _, c := range n.children {
		cs = append(cs, c)
	}
	slices.SortFunc(cs, func(a, b *pendingNode) int {
		if a.pass != b.pass {
			if a.pass < b.pass {
				return -1
			}
			return 1
		}
		if a.seq < b.seq {
			return -1
		}
		return 1
	})
	return cs
}

// flowPath returns the tree path of a gated trigger.
func (c *connections) flowPath(req *loops.TriggerRequest) [3]string {
	meta := req.Job.GetMetadata()
	actorType := meta.GetTarget().GetActor().GetType()
	return [3]string{c.appKey(actorType), actorType, meta.GetConcurrencyKey()}
}

func (c *connections) pendingFlow(path [3]string) *pendingNode {
	app, ok := c.pending.children[path[0]]
	if !ok {
		return nil
	}
	typ, ok := app.children[path[1]]
	if !ok {
		return nil
	}
	return typ.children[path[2]]
}

// enqueuePending adds a trigger to the end of its flow.
func (c *connections) enqueuePending(req *loops.TriggerRequest) {
	path := c.flowPath(req)
	c.pendingSeq++
	node := &c.pending
	node.size++
	for _, key := range path {
		node = node.child(key, c.pendingSeq)
		node.size++
	}
	node.items = append(node.items, pendingItem{req: req, arrived: c.now})

	c.recordPending(path[1])
}

// drain dispatches pending triggers until none can acquire their gates.
func (c *connections) drain() {
	for {
		dispatched, removed := c.pick(&c.pending, 0, [3]string{})
		if removed > 0 {
			c.pending.size -= removed
			c.recordPending("")
		}
		if !dispatched {
			return
		}
	}
}

// pick serves at most one trigger from the subtree in pass order, returning
// whether it dispatched one and how many triggers left the subtree (a
// trigger whose target has gone is resolved undeliverable on the way). A
// child that cannot dispatch is skipped without being charged.
func (c *connections) pick(n *pendingNode, depth int, path [3]string) (bool, int) {
	removed := 0
	for _, child := range n.byPass() {
		path[depth] = child.key

		var dispatched bool
		var r int
		if depth+1 == depthFlow {
			dispatched, r = c.dispatchHead(child, path)
		} else {
			dispatched, r = c.pick(child, depth+1, path)
		}

		child.size -= r
		removed += r
		if dispatched {
			n.vclock = child.pass
			child.pass += stride1 / c.weightAt(depth+1, path)
		}
		if child.size == 0 {
			delete(n.children, child.key)
		}
		if r > 0 && depth+1 == depthActorType {
			c.recordPending(child.key)
		}
		if dispatched {
			return true, removed
		}
	}
	return false, removed
}

// dispatchHead dispatches the oldest trigger of a flow if it can acquire all
// of its gates.
func (c *connections) dispatchHead(flow *pendingNode, path [3]string) (bool, int) {
	removed := 0
	for len(flow.items) > 0 {
		head := flow.items[0]

		keys := c.gateKeysForTrigger(head.req)
		if _, ok := c.acquireGates(keys); !ok {
			return false, removed
		}

		flow.items[0] = pendingItem{}
		flow.items = flow.items[1:]
		removed++

		streamLoop, ok := c.getStreamLoop(head.req.Job.GetMetadata())
		if !ok {
			c.releaseGates(keys)
			head.req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
			continue
		}

		c.dispatchWithGates(streamLoop, head.req, keys, c.now.Sub(head.arrived))
		return true, removed
	}
	return false, removed
}

// weightAt is the weight of the node at depth on path: the app's niceness,
// an equal share for each actor type of an app, and the flow's own niceness
// falling back to its app's.
func (c *connections) weightAt(depth int, path [3]string) uint64 {
	switch depth {
	case depthApp:
		return weightOf(c.niceness[path[0]])
	case depthFlow:
		if n, ok := c.niceness[path[1]+":"+path[2]]; ok {
			return weightOf(n)
		}
		return weightOf(c.niceness[path[0]])
	default:
		return weightOf(0)
	}
}

// failPending resolves every pending trigger of the actor types for which
// keep returns false.
func (c *connections) failPending(keep func(actorType string) bool, result api.TriggerResponseResult) {
	for appKey, app := range c.pending.children {
		for typeKey, typ := range app.children {
			if keep(typeKey) {
				continue
			}
			for _, flow := range typ.children {
				for _, item := range flow.items {
					item.req.ResultFn(result)
				}
			}
			app.size -= typ.size
			c.pending.size -= typ.size
			delete(app.children, typeKey)
			monitoring.RecordConcurrencyPending(typeKey, 0)
		}
		if app.size == 0 {
			delete(c.pending.children, appKey)
		}
	}
	c.recordPending("")
}

// recordPending records the pending count of an actor type, and of the
// namespace when the namespace is gated. An empty actorType records only the
// namespace.
func (c *connections) recordPending(actorType string) {
	if actorType != "" {
		var size int
		if app, ok := c.pending.children[c.appKey(actorType)]; ok {
			if typ, ok := app.children[actorType]; ok {
				size = typ.size
			}
		}
		monitoring.RecordConcurrencyPending(actorType, int64(size))
	}
	if c.nsGate != nil {
		monitoring.RecordConcurrencyPending(c.nsKey, int64(c.pending.size))
	}
}
