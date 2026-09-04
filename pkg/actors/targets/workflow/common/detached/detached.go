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
// such as arming the reminder of an already committed inbox row, on a
// runtime-lifetime context rather than an invocation, claim, or wake context.
package detached

import (
	"context"
	"sync"
)

// Runner spawns goroutines on a root context and waits for them on Close.
type Runner struct {
	ctx    context.Context
	cancel context.CancelFunc
	lock   sync.Mutex
	closed bool
	wg     sync.WaitGroup
	keyed  sync.Map
}

// New returns a Runner whose goroutines are bounded by ctx.
func New(ctx context.Context) *Runner {
	ctx, cancel := context.WithCancel(ctx)
	return &Runner{ctx: ctx, cancel: cancel}
}

// Go runs fn in a new goroutine with the root context and reports whether it
// was started; it is not started once the Runner is closed or its context is
// done.
func (r *Runner) Go(fn func(ctx context.Context)) bool {
	r.lock.Lock()
	if r.closed || r.ctx.Err() != nil {
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

// GoKeyed is Go for work identified by key: while a goroutine for key is in
// flight further calls are dropped and report inflight.
func (r *Runner) GoKeyed(key string, fn func(ctx context.Context)) (started, inflight bool) {
	if _, loaded := r.keyed.LoadOrStore(key, struct{}{}); loaded {
		return false, true
	}
	started = r.Go(func(ctx context.Context) {
		defer r.keyed.Delete(key)
		fn(ctx)
	})
	if !started {
		r.keyed.Delete(key)
	}
	return started, false
}

// InFlight reports whether a goroutine for key is running.
func (r *Runner) InFlight(key string) bool {
	_, ok := r.keyed.Load(key)
	return ok
}

// Close stops accepting work, cancels the root context and waits for every
// started goroutine.
func (r *Runner) Close() {
	r.lock.Lock()
	r.closed = true
	r.cancel()
	r.lock.Unlock()
	r.wg.Wait()
}

// Wait blocks until every started goroutine has returned.
func (r *Runner) Wait() {
	r.wg.Wait()
}
