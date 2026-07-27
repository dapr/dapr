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

type Pending struct {
	mu sync.Mutex
	m  map[string]chan Result
}

func New() *Pending {
	return &Pending{
		m: make(map[string]chan Result),
	}
}

// Register adds a waiter for the given execution key and returns the channel
// the result will be delivered on, plus a deregister function. A previous
// waiter registered under the same key (a superseded execution attempt) is
// cancelled and replaced.
func (p *Pending) Register(key string) (<-chan Result, func()) {
	ch := make(chan Result, 1)

	p.mu.Lock()
	if old, ok := p.m[key]; ok {
		old <- Result{Cancelled: true}
	}
	p.m[key] = ch
	p.mu.Unlock()

	return ch, func() {
		p.mu.Lock()
		if cur, ok := p.m[key]; ok && cur == ch {
			delete(p.m, key)
		}
		p.mu.Unlock()
	}
}

// Deliver hands a completion to the waiter registered for key, reporting
// whether a waiter was present. The waiter is deregistered on delivery.
func (p *Pending) Deliver(key string, data []byte) bool {
	return p.deliver(key, Result{Data: data})
}

// Cancel cancels the waiter registered for key, reporting whether a waiter
// was present. The waiter is deregistered on delivery.
func (p *Pending) Cancel(key string) bool {
	return p.deliver(key, Result{Cancelled: true})
}

func (p *Pending) deliver(key string, res Result) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, ok := p.m[key]
	if !ok {
		return false
	}
	ch <- res
	delete(p.m, key)

	return true
}

func (p *Pending) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.m)
}
