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
)

type Options struct {
	OnTimeout func(uint64)
	Timeout   time.Duration
}

type scheduledTimer struct {
	timer *time.Timer
	done  sync.Once
}

func (s *scheduledTimer) finish(callbacks *sync.WaitGroup) {
	s.done.Do(callbacks.Done)
}

// Timeout manages the single active timeout for a placement dissemination
// round. Enqueue replaces the current round, while Dequeue cancels it only
// when the supplied version still owns the timer.
type Timeout struct {
	onTimeout func(uint64)
	timeout   time.Duration

	lock      sync.Mutex
	timer     *scheduledTimer
	version   uint64
	closed    bool
	callbacks sync.WaitGroup
}

func New(opts Options) *Timeout {
	return &Timeout{
		onTimeout: opts.OnTimeout,
		timeout:   opts.Timeout,
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
	// event. Wait for it so the event cannot outlive its owning event loop.
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
	// supersedes any existing timer.
	t.cancelLocked()
	t.version = version
	scheduled := new(scheduledTimer)
	t.callbacks.Add(1)
	t.timer = scheduled
	scheduled.timer = time.AfterFunc(t.timeout, func() {
		defer scheduled.finish(&t.callbacks)

		t.lock.Lock()
		if t.closed || t.timer != scheduled {
			t.lock.Unlock()
			return
		}

		t.timer = nil
		t.lock.Unlock()
		t.onTimeout(version)
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
	if t.timer == nil {
		return
	}

	scheduled := t.timer
	t.timer = nil
	if scheduled.timer.Stop() {
		scheduled.finish(&t.callbacks)
	}
}
