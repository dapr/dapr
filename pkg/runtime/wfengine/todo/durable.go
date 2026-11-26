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
	"slices"
	"sync"
)

const (
	// DurableProperty holds the *Durable of a work item.
	DurableProperty = "dapr.durable"

	// DurableHeader is the response header daprd sets on a completion call
	// it answered only after the result was saved.
	DurableHeader = "dapr-workflow-durable"
)

// Durable reports whether the result a worker returned for a work item was
// saved. The actor that scheduled the work item settles it once the result
// is saved, or with the error that stopped the save.
type Durable struct {
	lock sync.Mutex
	err  error
	done chan struct{}
}

func NewDurable() *Durable {
	return &Durable{done: make(chan struct{})}
}

// Settle records the outcome: nil once the result is saved, or the error
// that stopped the save. Only the first call counts.
func (d *Durable) Settle(err error) {
	d.lock.Lock()
	defer d.lock.Unlock()
	select {
	case <-d.done:
	default:
		d.err = err
		close(d.done)
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

// Durables hands a completion call the Durable of the work item its result
// ended in. The engine completes or abandons a work item on the goroutine
// that delivers the result, so a call expects its result before delivering
// it, and finds the Durable on its Receipt once the delivery returns.
//
// A call is matched to a work item by the completion token of the work
// item's dispatch, which the worker echoes. A call whose result has no token
// is matched by its task key instead, so a work item is handed to the calls
// expecting either. That is exact because a result without a token only
// ever reaches a single pending execution of its task: the backends either
// keep one waiter per task or refuse the result while several are pending.
type Durables struct {
	lock    sync.Mutex
	waiting map[string][]*Receipt
}

func NewDurables() *Durables {
	return &Durables{waiting: make(map[string][]*Receipt)}
}

// Receipt is a completion call's expectation of a Durable.
type Receipt struct {
	durables *Durables
	key      string
	// Guarded by durables.lock.
	durable *Durable
}

func durableKey(key, token string) string {
	if token != "" {
		return "token/" + token
	}
	return "key/" + key
}

// Expect registers a completion call for the result of task key with the
// given completion token.
func (d *Durables) Expect(key, token string) *Receipt {
	r := &Receipt{durables: d, key: durableKey(key, token)}
	d.lock.Lock()
	d.waiting[r.key] = append(d.waiting[r.key], r)
	d.lock.Unlock()
	return r
}

// Fulfil hands durable to the calls that expect the result of task key,
// with the completion token of its dispatch or without a token.
func (d *Durables) Fulfil(key, token string, durable *Durable) {
	d.lock.Lock()
	defer d.lock.Unlock()
	fill := func(rs []*Receipt) {
		for _, r := range rs {
			if r.durable == nil {
				r.durable = durable
			}
		}
	}
	fill(d.waiting[durableKey(key, "")])
	if token != "" {
		fill(d.waiting[durableKey(key, token)])
	}
}

// Close ends the expectation, and returns the Durable of the work item the
// result ended in, or nil when no work item on this daprd took it.
func (r *Receipt) Close() *Durable {
	d := r.durables
	d.lock.Lock()
	defer d.lock.Unlock()
	rs := slices.DeleteFunc(d.waiting[r.key], func(o *Receipt) bool { return o == r })
	if len(rs) == 0 {
		delete(d.waiting, r.key)
	} else {
		d.waiting[r.key] = rs
	}
	return r.durable
}
