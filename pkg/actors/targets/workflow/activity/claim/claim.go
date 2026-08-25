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
	"errors"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/actors/state"
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
// HeartbeatMs is the writer's clock and readers never compare it against
// their own: staleness is judged by observing the value unchanged for
// StaleAfter on the reader's clock (see observeStale), so cross-host clock
// skew cannot reclaim a live execution.
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
	// StaleAfter is how long a reader must observe an unchanged heartbeat
	// value before the record reads as dead and the new owner reclaims.
	StaleAfter time.Duration
}

// Guards owns the guard goroutines and the record gate.
type Guards struct {
	opts Options

	lock   sync.Mutex
	active map[string]struct{}
	wg     sync.WaitGroup

	// observed tracks, per actor, when this reader first saw the record's
	// current heartbeat value; see observeStale.
	obsLock  sync.Mutex
	observed map[string]*observation
}

// observation is one reader-side staleness watch: firstSeen anchors the
// StaleAfter window for the recorded heartbeat value, lastSeen bounds the
// map (entries not refreshed within twice the grace are pruned).
type observation struct {
	taskKey     string
	heartbeatMs int64
	firstSeen   time.Time
	lastSeen    time.Time
}

func New(opts Options) *Guards {
	return &Guards{opts: opts}
}
