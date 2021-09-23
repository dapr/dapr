// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

type BlockQueue struct {
	input        chan interface{}
	output       chan interface{}
	values       []interface{}
	emptyWaiting chan interface{}
}

func NewBlockQueue() *BlockQueue {
	queue := &BlockQueue{
		input:        make(chan interface{}),
		output:       make(chan interface{}),
		values:       make([]interface{}, 0),
		emptyWaiting: make(chan interface{}),
	}
	go queue.process()
	return queue
}

func (q *BlockQueue) process() {
	// wait input first
	value := <-q.input
	q.values = append(q.values, value)

	for {
		if len(q.values) == 0 {
			select {
			case value := <-q.input:
				q.values = append(q.values, value)
			case q.emptyWaiting <- 1:
				continue
			}
		}

		front := q.values[0]
		select {
		case value := <-q.input:
			q.values = append(q.values, value)
		case q.output <- front:
			q.values = q.values[1:]
		}
	}
}

func (q *BlockQueue) Push(val interface{}) {
	q.input <- val
}

func (q *BlockQueue) Pop() interface{} {
	return <-q.output
}

func (q *BlockQueue) BlockUntilEmpty() {
	<-q.emptyWaiting
}
