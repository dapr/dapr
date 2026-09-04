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
)

// Check classifies the record for a fresh-owner recovery arrival about to
// execute taskKey.
func (g *Guards) Check(ctx context.Context, actorID, taskKey string) (Outcome, error) {
	rec, etag, err := g.read(ctx, actorID)
	if err != nil {
		return Defer, err
	}
	if rec == nil {
		g.forget(actorID)
		return Proceed, nil
	}
	stale := g.observeStale(actorID, rec)
	if rec.TaskKey != taskKey {
		// Another scheduling's record: ignore it, and reap it once its
		// guard host reads dead so an old run's row cannot leak forever.
		if stale && g.delete(ctx, actorID, etag) != nil {
			// The row changed under the read (a guard heartbeat won the
			// race): defer so the retry re-observes it. Not an error, a
			// classification.
			return Defer, nil //nolint:nilerr
		}
		if stale {
			g.forget(actorID)
		}
		return Proceed, nil
	}
	if rec.Completed {
		// Ack without executing; the result was already published. Reap the
		// row once it reads stale (a restart inside the retention window
		// leaves it behind: the guard's retention delete cannot run again),
		// best effort, so it does not linger for good.
		if stale {
			if g.delete(ctx, actorID, etag) == nil {
				g.forget(actorID)
			}
		}
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
	g.forget(actorID)
	return Proceed, nil
}

// observeStale reports whether rec's heartbeat has been observed unchanged
// for StaleAfter on this reader's clock. HeartbeatMs is another host's wall
// clock, so it is never compared against local time (skew of the grace or
// more would insta-reclaim a live execution); the first sight of a value
// only opens the window and a changed value reopens it. Recovery arrivals
// recur (janitor re-dispatch, deferral retry), so the multi-read watch
// converges within roughly one extra grace.
func (g *Guards) observeStale(actorID string, rec *Record) bool {
	now := time.Now()
	g.obsLock.Lock()
	defer g.obsLock.Unlock()
	for id, o := range g.observed {
		if now.Sub(o.lastSeen) > 2*g.opts.StaleAfter {
			delete(g.observed, id)
		}
	}
	o, ok := g.observed[actorID]
	if !ok || o.taskKey != rec.TaskKey || o.heartbeatMs != rec.HeartbeatMs {
		if g.observed == nil {
			g.observed = make(map[string]*observation)
		}
		g.observed[actorID] = &observation{
			taskKey:     rec.TaskKey,
			heartbeatMs: rec.HeartbeatMs,
			firstSeen:   now,
			lastSeen:    now,
		}
		return false
	}
	o.lastSeen = now
	return now.Sub(o.firstSeen) >= g.opts.StaleAfter
}

// forget drops the staleness watch once the record resolved (reclaimed,
// completed, or gone).
func (g *Guards) forget(actorID string) {
	g.obsLock.Lock()
	delete(g.observed, actorID)
	g.obsLock.Unlock()
}
