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
	"errors"
	"sync"
	"sync/atomic"

	clocklib "github.com/benbjohnson/clock"
)

var ErrProcessorStopped = errors.New("processor is stopped")

// Processor manages the queue of reminders and processes them at the correct time.
type Processor struct {
	executeFn          func(r *Reminder)
	queue              *Queue
	clock              clocklib.Clock
	lock               sync.Mutex
	processorRunningCh chan struct{}
	stopCh             chan struct{}
	stopped            atomic.Bool
}

// NewProcessor returns a new Processor object.
// executeFn is the callback invoked when the reminder is to be executed; this will be invoked in a background goroutine.
func NewProcessor(executeFn func(r *Reminder), clock clocklib.Clock) *Processor {
	queue := NewQueue()

	return &Processor{
		executeFn:          executeFn,
		queue:              &queue,
		processorRunningCh: make(chan struct{}, 1),
		stopCh:             make(chan struct{}),
		clock:              clock,
	}
}

// Enqueue adds a new reminder to the queue.
// If a reminder with the same ID already exists, it'll be replaced.
func (p *Processor) Enqueue(r *Reminder) error {
	if p.stopped.Load() {
		return ErrProcessorStopped
	}

	// Insert or replace the reminder in the queue
	// If the reminder added or replaced is the first one in the queue, we need to know that
	p.lock.Lock()
	peek := p.queue.Peek()
	isFirst := (peek != nil && peek.Key() == r.Key()) // This is going to be true if the reminder being replaced is the first one in the queue
	p.queue.Insert(r, true)
	isFirst = isFirst || (p.queue.Peek() == r) // This is also going to be true if the reminder just added landed at the front of the queue
	p.process(isFirst)
	p.lock.Unlock()

	return nil
}

// Dequeue removes a reminder from the queue
func (p *Processor) Dequeue(r *Reminder) error {
	if p.stopped.Load() {
		return ErrProcessorStopped
	}

	// We need to check if this is the next reminder in the queue, as that requires stopping the processor
	p.lock.Lock()
	peek := p.queue.Peek()
	p.queue.Remove(r)
	if peek != nil && peek.Key() == r.Key() {
		// If the reminder was the first one in the queue, restart the processor
		p.process(true)
	}
	p.lock.Unlock()

	return nil
}

// Stop the processor.
func (p *Processor) Stop() {
	if !p.stopped.CompareAndSwap(false, true) {
		// Already stopped
		return
	}

	// We need a lock because we're closing stopCh
	p.lock.Lock()
	close(p.stopCh)
	p.lock.Unlock()
}

// Start the processing loop if it's not already running.
// This must be invoked while the caller has a lock.
func (p *Processor) process(isNext bool) {
	if isNext {
		// If this is the next reminder, stop the processor if it's running, then restart it
		p.stopLoop()

		// This will block until the processor is available
		p.processorRunningCh <- struct{}{}
		go p.processLoop()
		return
	}

	// Do not start a loop if it's already running
	select {
	case p.processorRunningCh <- struct{}{}:
		// nop - fallthrough
	default:
		return
	}

	go p.processLoop()
}

// Sends a signal that stops the processor loop.
// This must be invoked while the caller has a lock.
func (p *Processor) stopLoop() {
	close(p.stopCh)
	p.stopCh = make(chan struct{})
}

// Processing loop.
func (p *Processor) processLoop() {
	var (
		r      *Reminder
		t      *clocklib.Timer
		stopCh chan struct{}
	)

loop:
	for {
		// Continue processing reminders until the queue is empty
		p.lock.Lock()
		stopCh = p.stopCh
		r = p.queue.Peek()
		p.lock.Unlock()
		if r == nil {
			break loop
		}

		t = p.clock.Timer(p.clock.Until(r.NextTick()))

		select {
		// Wait for when it's time to execute the reminder
		case <-t.C:
			// Pop the reminder now that we're ready to process it
			// There's a small chance this is a different reminder than the one we peeked before
			p.lock.Lock()
			// For safety, let's peek at the first reminder before popping it and make sure it's the same object
			// It's unlikely, but if it's a different object then restart the loop
			if p.queue.Peek() != r {
				p.lock.Unlock()
				continue
			}
			r = p.queue.Pop()
			p.lock.Unlock()
			if r == nil {
				break loop
			}

			go p.executeFn(r)

		// If we receive a stop signal, exit
		case <-stopCh:
			// Stop the timer and exit the loop
			t.Stop()
			break loop
		}
	}

	<-p.processorRunningCh
	return
}
