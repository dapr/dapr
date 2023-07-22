/*
Copyright 2023 The Dapr Authors
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

package internal

import (
	"container/heap"
	"time"
)

type queueable interface {
	comparable
	Key() string
	ScheduledTime() time.Time
}

// Queue implements a queue for Reminder and actor objects.
// It acts as a "priority queue", in which items are added in order of when they're scheduled.
// Internally, it uses a heap (from container/heap) that allows Insert and Pop operations to be completed in O(log N) time (where N is the queue's length).
// Note: methods in this struct are not safe for concurrent use. Callers should use locks to ensure consistency.
type Queue[T queueable] struct {
	heap  *queueHeap[T]
	items map[string]*queueItem[T]
}

// NewQueue creates a new queue.
func NewQueue[T queueable]() *Queue[T] {
	return &Queue[T]{
		heap:  &queueHeap[T]{},
		items: make(map[string]*queueItem[T]),
	}
}

// Len returns the number of items in the queue.
func (p *Queue[T]) Len() int {
	return p.heap.Len()
}

// Insert inserts a new item into the queue.
// If replace is true, existing items are replaced
func (p *Queue[T]) Insert(r T, replace bool) {
	key := r.Key()

	// Check if the item already exists
	item, ok := p.items[key]
	if ok {
		if replace {
			item.value = r
			heap.Fix(p.heap, item.index)
		}
		return
	}

	item = &queueItem[T]{
		value: r,
	}
	heap.Push(p.heap, item)
	p.items[key] = item
}

// Pop removes the next item in the queue and returns it.
// The returned boolean value will be "true" if an item was found.
func (p *Queue[T]) Pop() (T, bool) {
	if p.Len() == 0 {
		var zero T
		return zero, false
	}

	item, ok := heap.Pop(p.heap).(*queueItem[T])
	if !ok || item == nil {
		var zero T
		return zero, false
	}

	delete(p.items, item.value.Key())
	return item.value, true
}

// Peek returns the next item in the queue, without removing it.
// The returned boolean value will be "true" if an item was found.
func (p *Queue[T]) Peek() (T, bool) {
	if p.Len() == 0 {
		var zero T
		return zero, false
	}

	return (*p.heap)[0].value, true
}

// Remove an item from the queue.
func (p *Queue[T]) Remove(r T) {
	key := r.Key()

	// If the item is not in the queue, this is a nop
	item, ok := p.items[key]
	if !ok {
		return
	}

	heap.Remove(p.heap, item.index)
	delete(p.items, key)
}

// Update an item in the queue.
func (p *Queue[T]) Update(r T) {
	// If the item is not in the queue, this is a nop
	item, ok := p.items[r.Key()]
	if !ok {
		return
	}

	item.value = r
	heap.Fix(p.heap, item.index)
}

type queueItem[T queueable] struct {
	value T

	// The index of the item in the heap. This is maintained by the heap.Interface methods.
	index int
}

type queueHeap[T queueable] []*queueItem[T]

func (pq queueHeap[T]) Len() int {
	return len(pq)
}

func (pq queueHeap[T]) Less(i, j int) bool {
	return pq[i].value.ScheduledTime().Before(pq[j].value.ScheduledTime())
}

func (pq queueHeap[T]) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *queueHeap[T]) Push(x any) {
	n := len(*pq)
	item := x.(*queueItem[T])
	item.index = n
	*pq = append(*pq, item)
}

func (pq *queueHeap[T]) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // Avoid memory leak
	item.index = -1 // For safety
	*pq = old[0 : n-1]
	return item
}
