// Copyright ©2018 The Gonum Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build go1.12

// TODO(kortschak): Merge this into nodes.go when go1.11 is no longer supported.

package iterator

import (
	"reflect"

	"gonum.org/v1/gonum/graph"
)

// Nodes implements the graph.Nodes interfaces.
// The iteration order of Nodes is randomized.
type Nodes struct {
	nodes reflect.Value
	iter  *reflect.MapIter
	pos   int
	curr  graph.Node
}

// NewNodes returns a Nodes initialized with the provided nodes, a
// map of node IDs to graph.Nodes. No check is made that the keys
// match the graph.Node IDs, and the map keys are not used.
//
// Behavior of the Nodes is unspecified if nodes is mutated after
// the call the NewNodes.
func NewNodes(nodes map[int64]graph.Node) *Nodes {
	rv := reflect.ValueOf(nodes)
	return &Nodes{nodes: rv, iter: rv.MapRange()}
}

// Len returns the remaining number of nodes to be iterated over.
func (n *Nodes) Len() int {
	return n.nodes.Len() - n.pos
}

// Next returns whether the next call of Node will return a valid node.
func (n *Nodes) Next() bool {
	if n.pos >= n.nodes.Len() {
		return false
	}
	ok := n.iter.Next()
	if ok {
		n.pos++
		n.curr = n.iter.Value().Interface().(graph.Node)
	}
	return ok
}

// Node returns the current node of the iterator. Next must have been
// called prior to a call to Node.
func (n *Nodes) Node() graph.Node {
	return n.curr
}

// Reset returns the iterator to its initial state.
func (n *Nodes) Reset() {
	n.curr = nil
	n.pos = 0
	n.iter = n.nodes.MapRange()
}
