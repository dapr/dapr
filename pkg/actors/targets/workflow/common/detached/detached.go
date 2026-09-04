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

// Package detached runs work that must outlive the invocation requesting it,
// such as arming the reminder of an already committed inbox row, on the
// runtime root context rather than an invocation, claim, or wake context.
package detached

import (
	"context"
	"sync"
)

// Runner spawns goroutines on a root context and waits for them.
type Runner struct {
	ctx  context.Context
	lock sync.Mutex
	wg   sync.WaitGroup
}

// New returns a Runner whose goroutines are bounded by ctx.
func New(ctx context.Context) *Runner {
	return &Runner{ctx: ctx}
}

// Go runs fn in a new goroutine with the root context and reports whether it
// was started; it is not started once the root context is done.
func (r *Runner) Go(fn func(ctx context.Context)) bool {
	r.lock.Lock()
	if r.ctx.Err() != nil {
		r.lock.Unlock()
		return false
	}
	r.wg.Add(1)
	r.lock.Unlock()

	go func() {
		defer r.wg.Done()
		fn(r.ctx)
	}()
	return true
}

// Wait blocks until every started goroutine has returned.
func (r *Runner) Wait() {
	r.wg.Wait()
}
