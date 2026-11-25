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

package actors

import (
	"context"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
)

// DurableHeader is the response header daprd sets on a completion call it
// answered only after the result was saved.
const DurableHeader = "dapr-workflow-durable"

// savedTokenTTL is at least how long a saved completion token is remembered,
// so that a worker that sends the same result again is told it was saved.
const savedTokenTTL = time.Minute

// durables tracks, per task key, the todo.Durable of each work item the engine
// holds, so that the answer to a completion call can wait for its result to
// be saved. It also remembers, per task key, the completion token of the last
// result it saw saved.
type durables struct {
	clock clock.Clock

	lock    sync.Mutex
	pending map[string][]*trackedDurable
	// saved and savedPrev map a task key to the completion token of its last
	// saved result. saved replaces savedPrev every savedTokenTTL.
	saved     map[string]string
	savedPrev map[string]string
	rotatedAt time.Time
}

type trackedDurable struct {
	durable *todo.Durable
	// token is the completion token of the result the work item consumed,
	// once a completion call has seen it consumed. Guarded by durables.lock.
	token string
}

func newDurables(clk clock.Clock) *durables {
	return &durables{
		clock:     clk,
		pending:   make(map[string][]*trackedDurable),
		saved:     make(map[string]string),
		savedPrev: make(map[string]string),
		rotatedAt: clk.Now(),
	}
}

// track records the Durable of a work item handed to the engine until it is
// settled.
func (d *durables) track(key string, durable *todo.Durable) {
	entry := &trackedDurable{durable: durable}
	d.lock.Lock()
	d.pending[key] = append(d.pending[key], entry)
	d.lock.Unlock()

	durable.OnSettle(func() {
		d.lock.Lock()
		defer d.lock.Unlock()
		entries := slices.DeleteFunc(d.pending[key], func(e *trackedDurable) bool { return e == entry })
		if len(entries) == 0 {
			delete(d.pending, key)
		} else {
			d.pending[key] = entries
		}
	})
}

// unconsumed returns the tracked work items for key whose result has not been
// handed to the engine yet.
func (d *durables) unconsumed(key string) []*trackedDurable {
	d.lock.Lock()
	defer d.lock.Unlock()
	var out []*trackedDurable
	for _, e := range d.pending[key] {
		if !e.durable.Consumed() {
			out = append(out, e)
		}
	}
	return out
}

// adopted returns the entries of before whose result has been consumed since,
// and records token against them.
func (d *durables) adopted(before []*trackedDurable, token string) []*todo.Durable {
	d.lock.Lock()
	defer d.lock.Unlock()
	var out []*todo.Durable
	for _, e := range before {
		if e.durable.Consumed() {
			if e.token == "" {
				e.token = token
			}
			out = append(out, e.durable)
		}
	}
	return out
}

// consumedWith returns the tracked work item for key that consumed the result
// with the given completion token, if it is not settled yet.
func (d *durables) consumedWith(key, token string) *todo.Durable {
	d.lock.Lock()
	defer d.lock.Unlock()
	for _, e := range d.pending[key] {
		if e.token == token {
			return e.durable
		}
	}
	return nil
}

func (d *durables) markSaved(key, token string) {
	d.lock.Lock()
	defer d.lock.Unlock()
	if now := d.clock.Now(); now.Sub(d.rotatedAt) >= savedTokenTTL {
		d.savedPrev = d.saved
		d.saved = make(map[string]string)
		d.rotatedAt = now
	}
	d.saved[key] = token
}

func (d *durables) isSaved(key, token string) bool {
	d.lock.Lock()
	defer d.lock.Unlock()
	return d.saved[key] == token || d.savedPrev[key] == token
}

// completeDurably delivers a result with deliver and answers the worker only
// once the result is saved.
//
// The engine runs a delivered result through to Complete*WorkItem on the
// delivering goroutine, so a tracked work item that consumed its result while
// deliver ran took this one. The answer waits for each such work item to
// settle, and is Aborted when one was not saved. A result that no tracked
// work item took is answered as before: it was forwarded to another daprd,
// discarded as stale, or fed a turn that continues as new.
//
// When deliver finds no pending task, the result was taken earlier or
// dropped. The answer is then a success only if a result with the same
// completion token was saved, and NotFound otherwise. A result without a
// completion token cannot be matched, and is answered as a success as before.
func (abe *Actors) completeDurably(ctx context.Context, key, token string, deliver func() error) error {
	before := abe.durables.unconsumed(key)

	err := abe.callWithBackoff(ctx, deliver)
	switch {
	case isUnknownTask(err):
		return abe.answerUnknown(ctx, key, token, err)
	case err != nil:
		return err
	}

	adopted := abe.durables.adopted(before, token)
	if len(adopted) == 0 {
		return nil
	}
	for _, durable := range adopted {
		if werr := waitDurable(ctx, durable); werr != nil {
			return werr
		}
	}
	abe.saved(ctx, key, token)
	return nil
}

// answerUnknown answers a result for which deliver found no pending task.
func (abe *Actors) answerUnknown(ctx context.Context, key, token string, err error) error {
	if token == "" {
		log.Warnf("Ignoring complete task which no longer exists: %s", err)
		return nil
	}

	// The worker sent the result again while the first copy is being saved.
	if durable := abe.durables.consumedWith(key, token); durable != nil {
		if werr := waitDurable(ctx, durable); werr != nil {
			return werr
		}
		abe.saved(ctx, key, token)
		return nil
	}

	if abe.durables.isSaved(key, token) {
		abe.saved(ctx, key, token)
		return nil
	}

	return status.Errorf(codes.NotFound, "task %s is not pending and no result with this completion token was saved: %s", key, err)
}

func (abe *Actors) saved(ctx context.Context, key, token string) {
	if token != "" {
		abe.durables.markSaved(key, token)
	}
	// Not a gRPC server context when called in-process.
	_ = grpc.SetHeader(ctx, metadata.Pairs(DurableHeader, "true"))
}

func waitDurable(ctx context.Context, durable *todo.Durable) error {
	select {
	case <-durable.Done():
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
	if err := durable.Err(); err != nil {
		return status.Errorf(codes.Aborted, "result was not saved: %s", err)
	}
	return nil
}

func isUnknownTask(err error) bool {
	return api.IsUnknownTaskIDError(err) || api.IsUnknownInstanceIDError(err)
}
