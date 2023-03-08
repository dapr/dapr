/*
Copyright 2021 The Dapr Authors
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

package reminders

import (
	"container/heap"
)

// Queue implements a queue for Reminder objects.
// It acts as a "priority queue", in which reminders are added in order of when they're scheduled.
// Internally, it uses a heap (from container/heap) that allows Insert and Pop operations to be completed in O(log N) time (where N is the queue's length).
// Note: methods in this struct are not safe for concurrent use. Callers should use locks to ensure consistency.
type Queue struct {
	heap  *queueHeap
	items map[string]*queueItem
}

// NewQueue creates a new Reminder queue.
func NewQueue() Queue {
	return Queue{
		heap:  &queueHeap{},
		items: make(map[string]*queueItem),
	}
}

// Len returns the number of reminders in the queue.
func (p *Queue) Len() int {
	return p.heap.Len()
}

// Insert inserts a new reminder into the queue.
// If replace is true, existing reminders are replaced
func (p *Queue) Insert(r *Reminder, replace bool) {
	key := r.Key()

	// Check if the reminder already exists
	item, ok := p.items[key]
	if ok {
		if replace {
			item.value = r
			heap.Fix(p.heap, item.index)
		}
		return
	}

	item = &queueItem{
		value: r,
	}
	heap.Push(p.heap, item)
	p.items[key] = item
}

// Pop removes the next reminder in the queue and returns it, or nil if the queue is empty.
func (p *Queue) Pop() *Reminder {
	if p.Len() == 0 {
		return nil
	}

	item, ok := heap.Pop(p.heap).(*queueItem)
	if !ok || item == nil {
		return nil
	}

	delete(p.items, item.value.Key())
	return item.value
}

// Peek returns the next reminder in the queue, without removing it, or nil if the queue is empty.
func (p *Queue) Peek() *Reminder {
	if p.Len() == 0 {
		return nil
	}

	return (*p.heap)[0].value
}

// Remove a reminder from the queue.
func (p *Queue) Remove(r *Reminder) {
	key := r.Key()

	// If the item is not in the queue, this is a nop
	item, ok := p.items[key]
	if !ok {
		return
	}

	heap.Remove(p.heap, item.index)
	delete(p.items, key)
}

// Update a reminder in the queue.
func (p *Queue) Update(r *Reminder) {
	// If the item is not in the queue, this is a nop
	item, ok := p.items[r.Key()]
	if !ok {
		return
	}

	item.value = r
	heap.Fix(p.heap, item.index)
}

type queueItem struct {
	value *Reminder

	// The index of the item in the heap. This is maintained by the heap.Interface methods.
	index int
}

type queueHeap []*queueItem

func (pq queueHeap) Len() int {
	return len(pq)
}

func (pq queueHeap) Less(i, j int) bool {
	return pq[i].value.RegisteredTime.UnixNano() < pq[j].value.RegisteredTime.UnixNano()
}

func (pq queueHeap) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *queueHeap) Push(x any) {
	n := len(*pq)
	item := x.(*queueItem)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *queueHeap) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // Avoid memory leak
	item.index = -1 // For safety
	*pq = old[0 : n-1]
	return item
}
