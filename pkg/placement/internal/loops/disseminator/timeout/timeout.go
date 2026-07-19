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

package timeout

import (
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/kit/events/loop"
)

type Options struct {
	Loop    loop.Interface[loops.EventDisseminator]
	Timeout time.Duration
}

type scheduledTimer struct {
	timer *time.Timer
	done  sync.Once
}

func (s *scheduledTimer) finish(callbacks *sync.WaitGroup) {
	s.done.Do(callbacks.Done)
}

type Timeout struct {
	loop    loop.Interface[loops.EventDisseminator]
	timeout time.Duration

	lock       sync.Mutex
	timer      *scheduledTimer
	version    uint64
	generation uint64
	closed     bool
	callbacks  sync.WaitGroup
}

func New(opts Options) *Timeout {
	return &Timeout{
		loop:    opts.Loop,
		timeout: opts.Timeout,
	}
}

func (t *Timeout) Close() error {
	t.lock.Lock()
	if !t.closed {
		t.closed = true
		t.cancelLocked()
	}
	t.lock.Unlock()

	// A fired callback may already be between validation and enqueueing its
	// event. Wait for it so the event cannot outlive and target a recycled loop.
	t.callbacks.Wait()

	return nil
}

func (t *Timeout) Enqueue(version uint64) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.closed {
		return
	}

	// Dissemination has at most one active round, so a newly enqueued version
	// supersedes any existing timer. Keeping the timer alive independently of a
	// queue worker avoids losing the new deadline while that worker is exiting
	// after the previous version was dequeued.
	t.cancelLocked()
	t.version = version
	generation := t.generation
	scheduled := new(scheduledTimer)
	t.callbacks.Add(1)
	t.timer = scheduled
	scheduled.timer = time.AfterFunc(t.timeout, func() {
		defer scheduled.finish(&t.callbacks)

		t.lock.Lock()
		if t.closed || t.timer != scheduled || t.version != version || t.generation != generation {
			t.lock.Unlock()
			return
		}

		t.timer = nil
		t.lock.Unlock()
		t.loop.Enqueue(&loops.DisseminationTimeout{
			Version: version,
		})
	})
}

func (t *Timeout) Dequeue(version uint64) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.closed || t.timer == nil || t.version != version {
		return
	}

	t.cancelLocked()
}

func (t *Timeout) cancelLocked() {
	t.generation++
	if t.timer != nil {
		scheduled := t.timer
		t.timer = nil
		if scheduled.timer.Stop() {
			scheduled.finish(&t.callbacks)
		}
	}
}
