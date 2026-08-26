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

package inflight

import (
	"sync"
	"sync/atomic"
	"time"
)

// Call tracks a single in-flight activity execution.
type Call struct {
	phase   atomic.Int32
	done    chan struct{}
	once    sync.Once
	err     error
	created time.Time
}

func newCall() *Call {
	return &Call{done: make(chan struct{}), created: time.Now()}
}

// Age returns how long ago this call was claimed. Used by the stale-claim
// eviction check (see the activity target's claim).
func (c *Call) Age() time.Duration {
	return time.Since(c.created)
}

// Call phases: a call starts executing; the owner moves it to resolving when
// its engine work item completes and the result publish begins; a stale-claim
// check moves it to evicted. Resolve and evict both CAS out of executing, so
// exactly one transition wins: an eviction can never land mid-publish, and a
// resolve can never resurrect an evicted call.
const (
	phaseExecuting = int32(iota)
	phaseResolving
	phaseEvicted
)

// BeginResolve moves the call from executing to resolving: its engine work
// item has completed and the result is being published into the parent
// workflow. The engine's held registration is released at completion, so
// without this phase the publish window (which contends on the parent's
// turn lock) would read as not-held and a stale-claim check could evict a
// healthy execution. Returns false if an eviction won the race first.
func (c *Call) BeginResolve() bool {
	return c.phase.CompareAndSwap(phaseExecuting, phaseResolving)
}

// TryEvict moves the call from executing to evicted. Returns false if the
// call is resolving (or already evicted): the caller must then treat the
// claim as live.
func (c *Call) TryEvict() bool {
	return c.phase.CompareAndSwap(phaseExecuting, phaseEvicted)
}

// Resolving reports whether the call has entered the resolve phase.
func (c *Call) Resolving() bool {
	return c.phase.Load() == phaseResolving
}

// Settled reports whether Finish has been called.
func (c *Call) Settled() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Done returns a channel that is closed when Finish has been called. After
// Done is closed, Err reports the outcome.
func (c *Call) Done() <-chan struct{} {
	return c.done
}

// Err returns the outcome of the call. Only valid after Done is closed. A nil
// return means the activity completed successfully and its result has been
// published to the workflow actor by the owner; followers should surface nil
// to their caller so the scheduler acks SUCCESS.
func (c *Call) Err() error {
	return c.err
}

// Finish closes the call with the given outcome. Idempotent. Only the owner
// should call this.
func (c *Call) Finish(err error) {
	c.once.Do(func() {
		c.err = err
		close(c.done)
	})
}
