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

// Package claim implements the durable execution-claim record for activity
// actors under the WorkflowsFastPath preview: a lease written only around
// placement churn, so a recovery arrival on a new placement owner can tell a
// live cross-host execution, or its published result, from one that died
// with its host.
package claim

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.activity.claim")

const (
	// recordStateKey keys the record under the activity actor's state
	// prefix; TaskKey tells scheduling generations apart.
	recordStateKey = "execution-claim"

	// opTimeout bounds each guard state operation so a slow store cannot
	// park a guard goroutine indefinitely.
	opTimeout = 10 * time.Second
)

// ErrHeldElsewhere defers a recovery arrival whose body is still executing
// on the previous owner (live heartbeat). Recoverable: retries land after
// the record resolves to Completed, deleted, or stale.
var ErrHeldElsewhere = wferrors.NewRecoverable(errors.New(
	"activity execution claim is held live by another host; deferring re-execution"))

// Outcome classifies the record for a fresh-owner recovery arrival.
type Outcome int

const (
	// Proceed: no record, a different scheduling's record, or a stale one
	// (its guard host died); execute as a fresh owner.
	Proceed Outcome = iota
	// Defer: a live heartbeat proves the body is still executing on another
	// host; surface a recoverable error so the arrival retries.
	Defer
	// Completed: the guarded execution finished cleanly and its result is
	// published; ack success without executing.
	Completed
)

// Record is the persisted shape of the execution-claim state row.
type Record struct {
	TaskKey     string `json:"taskKey"`
	HeartbeatMs int64  `json:"heartbeatMs"`
	Completed   bool   `json:"completed"`
}

// Options configures Guards. HeartbeatEvery, Retention and StaleAfter are
// options only so tests can compress them.
type Options struct {
	ActorType string
	State     state.Interface
	// HeartbeatEvery paces the guard's record refresh.
	HeartbeatEvery time.Duration
	// Retention keeps a Completed record readable before deletion, the
	// durable analogue of the in-memory inflight cache TTL.
	Retention time.Duration
	// StaleAfter is the grace after the last heartbeat before a record
	// reads as dead and the new owner reclaims.
	StaleAfter time.Duration
}

// Guards owns the guard goroutines and the record gate.
type Guards struct {
	opts Options

	lock   sync.Mutex
	active map[string]struct{}
	wg     sync.WaitGroup
}

func New(opts Options) *Guards {
	return &Guards{opts: opts}
}

// Spawn writes the record synchronously and starts one heartbeat goroutine
// per task key (deduped; rootCtx-bounded, waitgroup-accounted). The
// synchronous write runs within HaltNonHosted's UPDATE phase, so the record
// is durable before UNLOCK admits the first recovery arrival.
func (g *Guards) Spawn(ctx, rootCtx context.Context, actorID, taskKey string, call *inflight.Call) {
	g.lock.Lock()
	if rootCtx.Err() != nil {
		g.lock.Unlock()
		return
	}
	if g.active == nil {
		g.active = make(map[string]struct{})
	}
	if _, ok := g.active[taskKey]; ok {
		g.lock.Unlock()
		return
	}
	g.active[taskKey] = struct{}{}
	g.wg.Add(1)
	g.lock.Unlock()

	log.Infof("Activity actor '%s': guarding its in-flight execution claim across placement churn", actorID)

	g.write(ctx, actorID, taskKey, false)

	go func() {
		defer func() {
			g.lock.Lock()
			delete(g.active, taskKey)
			g.lock.Unlock()
			g.wg.Done()
		}()
		g.guard(rootCtx, actorID, taskKey, call)
	}()
}

// Wait blocks until all guard goroutines have exited.
func (g *Guards) Wait() {
	g.wg.Wait()
}

// Active returns the number of running guards.
func (g *Guards) Active() int {
	g.lock.Lock()
	defer g.lock.Unlock()
	return len(g.active)
}

// guard heartbeats the already-written record every HeartbeatEvery until the
// local execution settles: clean finish marks Completed and retains the
// record for Retention before deleting; a failed finish deletes immediately;
// a dead host goes stale. Stale and deleted both mean the new owner
// re-executes.
func (g *Guards) guard(ctx context.Context, actorID, taskKey string, call *inflight.Call) {
	ticker := time.NewTicker(g.opts.HeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Process shutdown: the execution dies with this host, so the
			// record only stalls the new owner. Best-effort delete; a lost
			// delete goes stale within the grace anyway.
			g.deleteOwned(context.WithoutCancel(ctx), actorID, taskKey)
			return
		case <-call.Done():
			if call.Err() != nil {
				g.deleteOwned(ctx, actorID, taskKey)
				return
			}
			g.write(ctx, actorID, taskKey, true)
			select {
			case <-ctx.Done():
			case <-time.After(g.opts.Retention):
				g.deleteOwned(ctx, actorID, taskKey)
			}
			return
		case <-ticker.C:
			g.write(ctx, actorID, taskKey, false)
		}
	}
}

// deleteOwned deletes the record only while it still carries taskKey: a
// newer scheduling's guard may have overwritten the shared row, and deleting
// that would unprotect a live execution. The delete is ETag-conditional so a
// write landing between the read and the delete also keeps the newer row.
func (g *Guards) deleteOwned(ctx context.Context, actorID, taskKey string) {
	rec, etag, err := g.read(ctx, actorID)
	if err != nil {
		log.Warnf("Activity actor '%s': failed to read the execution-claim record before deleting; leaving it to go stale: %v", actorID, err)
		return
	}
	if rec == nil || rec.TaskKey != taskKey {
		return
	}
	g.delete(ctx, actorID, etag)
}

// Check classifies the record for a fresh-owner recovery arrival about to
// execute taskKey.
func (g *Guards) Check(ctx context.Context, actorID, taskKey string) (Outcome, error) {
	rec, etag, err := g.read(ctx, actorID)
	if err != nil {
		return Defer, err
	}
	if rec == nil {
		return Proceed, nil
	}
	stale := time.Since(time.UnixMilli(rec.HeartbeatMs)) >= g.opts.StaleAfter
	if rec.TaskKey != taskKey {
		// Another scheduling's record: ignore it, and reap it when its
		// guard host is dead so an old run's row cannot leak forever.
		if stale && g.delete(ctx, actorID, etag) != nil {
			// The row changed under the read (a guard heartbeat won the
			// race): defer so the retry re-observes it. Not an error, a
			// classification.
			return Defer, nil //nolint:nilerr
		}
		return Proceed, nil
	}
	if rec.Completed {
		return Completed, nil
	}
	if !stale {
		return Defer, nil
	}
	// The guarding host stopped heartbeating: it is dead, reclaim. The
	// ETag-conditional delete closes the read/delete race: a heartbeat
	// landing in between fails the delete, and the arrival defers instead
	// of duplicating the revived execution.
	if g.delete(ctx, actorID, etag) != nil {
		// Reclaim lost the race; classify as live rather than error.
		return Defer, nil //nolint:nilerr
	}
	return Proceed, nil
}

func (g *Guards) write(ctx context.Context, actorID, taskKey string, completed bool) {
	octx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	err := g.opts.State.TransactionalStateOperation(octx, true, &actorsapi.TransactionalRequest{
		ActorType: g.opts.ActorType,
		ActorID:   actorID,
		Operations: []actorsapi.TransactionalOperation{{
			Operation: actorsapi.Upsert,
			Request: actorsapi.TransactionalUpsert{
				Key: recordStateKey,
				Value: Record{
					TaskKey:     taskKey,
					HeartbeatMs: time.Now().UnixMilli(),
					Completed:   completed,
				},
			},
		}},
	}, false)
	if err != nil {
		log.Warnf("Activity actor '%s': failed to write the execution-claim record; recovery degrades to at-least-once for this handoff: %v", actorID, err)
	}
}

// delete removes the record, conditional on etag when the read that observed
// it returned one, so a concurrent overwrite is kept rather than destroyed.
func (g *Guards) delete(ctx context.Context, actorID string, etag *string) error {
	octx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	err := g.opts.State.TransactionalStateOperation(octx, true, &actorsapi.TransactionalRequest{
		ActorType: g.opts.ActorType,
		ActorID:   actorID,
		Operations: []actorsapi.TransactionalOperation{{
			Operation: actorsapi.Delete,
			Request:   actorsapi.TransactionalDelete{Key: recordStateKey, ETag: etag},
		}},
	}, false)
	if err != nil {
		log.Warnf("Activity actor '%s': failed to delete the execution-claim record: %v", actorID, err)
	}
	return err
}

func (g *Guards) read(ctx context.Context, actorID string) (*Record, *string, error) {
	res, err := g.opts.State.Get(ctx, &actorsapi.GetStateRequest{
		ActorType: g.opts.ActorType,
		ActorID:   actorID,
		Key:       recordStateKey,
	}, false)
	if err != nil {
		return nil, nil, err
	}
	if res == nil || len(res.Data) == 0 {
		return nil, nil, nil
	}
	var rec Record
	if err := json.Unmarshal(res.Data, &rec); err != nil {
		return nil, nil, err
	}
	return &rec, res.ETag, nil
}
