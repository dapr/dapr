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

// Package pending implements a process-local rendezvous between workflow and
// activity task waiters and the completions reported by the application.
// Under WorkflowsClusteredDeployment the waiter always runs on the daprd
// hosting the workflow or activity actor, and the executor rendezvous actor
// is placed on that same host because it shares the actor ID (placement
// hashes only the actor ID). Registering the waiter here lets a completion
// arriving on this host, either directly or via the executor actor, be
// delivered in-process without a watch stream.
package pending

import (
	"sync"
)

// Result is a delivered task completion. Data is the raw marshaled
// ActivityResponse or WorkflowResponse; Cancelled reports that the task was
// cancelled instead of completed.
type Result struct {
	Data      []byte
	Cancelled bool
}

// waiter is a single registration: a callback invoked with the result on the
// delivering goroutine.
type waiter struct {
	cb func(Result)
}

type Pending struct {
	mu sync.Mutex
	m  map[string]*waiter
}

func New() *Pending {
	return &Pending{
		m: make(map[string]*waiter),
	}
}

// RegisterCallback adds a callback waiter for the given execution key and
// returns a deregister function. The callback runs on the goroutine
// delivering the completion or cancellation, never with the registry lock
// held. A registration STAYS ARMED across deliveries and is removed only by
// its deregister function (or by being superseded): the consumer may discard
// a delivery as stale (durabletask's completion-token guard) and keep
// waiting, so consuming the registration on delivery would strand the
// genuine completion with nowhere to land. The callback may therefore run
// more than once; the consumer's arbiter settles exactly one delivery. A
// superseded waiter's cancellation runs on the registering goroutine.
func (p *Pending) RegisterCallback(key string, cb func(Result)) func() {
	w := &waiter{cb: cb}
	p.register(key, w)
	return p.deregisterFunc(key, w)
}

func (p *Pending) register(key string, w *waiter) {
	p.mu.Lock()
	old := p.m[key]
	p.m[key] = w
	p.mu.Unlock()

	if old != nil {
		old.cb(Result{Cancelled: true})
	}
}

func (p *Pending) deregisterFunc(key string, w *waiter) func() {
	return func() {
		p.mu.Lock()
		if cur, ok := p.m[key]; ok && cur == w {
			delete(p.m, key)
		}
		p.mu.Unlock()
	}
}

// Deliver hands a completion to the waiter registered for key, reporting
// whether a waiter was present. The waiter stays registered (see
// RegisterCallback) and its callback runs on the calling goroutine before
// Deliver returns.
func (p *Pending) Deliver(key string, data []byte) bool {
	run, ok := p.DeliverDeferred(key, data)
	if run != nil {
		run()
	}
	return ok
}

// Cancel cancels the waiter registered for key, reporting whether a waiter
// was present, with the same deregistration semantics as Deliver.
func (p *Pending) Cancel(key string) bool {
	run, ok := p.CancelDeferred(key)
	if run != nil {
		run()
	}
	return ok
}

// DeliverDeferred is Deliver for callers holding their own locks: the
// waiter's invocation is returned as run for the caller to execute after
// releasing them.
func (p *Pending) DeliverDeferred(key string, data []byte) (run func(), ok bool) {
	return p.take(key, Result{Data: data})
}

// CancelDeferred is Cancel with the DeliverDeferred contract.
func (p *Pending) CancelDeferred(key string) (run func(), ok bool) {
	return p.take(key, Result{Cancelled: true})
}

// take delivers res to the waiter for key. The waiter stays registered (see
// RegisterCallback) and its invocation is handed back as a thunk so it never
// runs under the registry lock (the continuation it carries can re-enter
// this registry or the caller's locks).
func (p *Pending) take(key string, res Result) (func(), bool) {
	p.mu.Lock()
	w, ok := p.m[key]
	p.mu.Unlock()
	if !ok {
		return nil, false
	}
	return func() { w.cb(res) }, true
}

func (p *Pending) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.m)
}
