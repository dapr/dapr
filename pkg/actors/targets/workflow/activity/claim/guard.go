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

package claim

import (
	"context"
	"time"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
)

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
				// Shutdown mid-retention: keep the Completed row so recovery
				// arrivals on the new owner ack instead of re-executing the
				// published body; gate reads reap it once it goes stale.
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
