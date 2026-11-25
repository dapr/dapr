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

package todo

import (
	"sync"
	"sync/atomic"
)

// DurableProperty holds the *Durable of a work item.
const DurableProperty = "dapr.durable"

// Durable reports whether the result a worker returned for a work item was
// saved. The backend marks it consumed when the engine hands the result to
// the actor that scheduled the work item. That actor settles it once the
// result is saved, or with the error that stopped the save. The backend
// waits for it before it answers the worker.
type Durable struct {
	consumed atomic.Bool

	lock     sync.Mutex
	settled  bool
	err      error
	done     chan struct{}
	onSettle func()
}

func NewDurable() *Durable {
	return &Durable{done: make(chan struct{})}
}

// Consume records that the engine handed the work item's result to the
// actor.
func (d *Durable) Consume() {
	d.consumed.Store(true)
}

// Consumed reports whether Consume was called.
func (d *Durable) Consumed() bool {
	return d.consumed.Load()
}

// Settle records the outcome: nil once the result is saved, or the error
// that stopped the save. Only the first call counts.
func (d *Durable) Settle(err error) {
	d.lock.Lock()
	if d.settled {
		d.lock.Unlock()
		return
	}
	d.settled = true
	d.err = err
	close(d.done)
	fn := d.onSettle
	d.lock.Unlock()

	if fn != nil {
		fn()
	}
}

// Done is closed once the outcome is settled.
func (d *Durable) Done() <-chan struct{} {
	return d.done
}

// Err returns the settled outcome. It is only meaningful after Done is
// closed.
func (d *Durable) Err() error {
	d.lock.Lock()
	defer d.lock.Unlock()
	return d.err
}

// OnSettle runs fn once the outcome is settled, at once if it already is.
// Only the last fn given is run.
func (d *Durable) OnSettle(fn func()) {
	d.lock.Lock()
	if d.settled {
		d.lock.Unlock()
		fn()
		return
	}
	d.onSettle = fn
	d.lock.Unlock()
}
