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
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"
)

const (
	// fallbackWaitingWarnAfter is how long a daprd stays a fallback host
	// before it warns about the operations it staged, which wait for a
	// daprd of the app with a workflow worker. The warnings then come at
	// doubling intervals since it became a fallback host, capped at
	// fallbackWaitingWarnMaxInterval.
	fallbackWaitingWarnAfter       = 5 * time.Minute
	fallbackWaitingWarnMaxInterval = time.Hour
)

// SetFallbackHost tells the backend whether this daprd is a fallback host.
// While it is, the operations it stages are counted, and a warning is logged
// on a schedule once any has been. ctx bounds the warnings; they stop
// once it is done or the daprd stops being a fallback host. Called by the
// engine under its actor registration lock.
func (abe *Actors) SetFallbackHost(ctx context.Context, fallback bool) {
	if !fallback {
		// The operations staged since the last executor left run here, as
		// one connected, or on the daprd the instances move to.
		abe.fallbackStagedEarly.Store(0)
	}

	current := abe.fallbackWaiting.Load()
	switch {
	case fallback && current == nil && ctx != nil && ctx.Err() == nil:
		wctx, cancel := context.WithCancel(ctx)
		w := newFallbackWaiting(abe.appID)
		w.staged.Store(abe.fallbackStagedEarly.Swap(0))
		w.cancel = cancel
		abe.fallbackWaiting.Store(w)
		go w.run(wctx)
	case !fallback && current != nil:
		abe.fallbackWaiting.Store(nil)
		current.cancel()
	}
}

// fallbackServed counts an operation served by this daprd as a fallback
// host; see SetFallbackHost.
func (abe *Actors) fallbackServed() {
	if w := abe.fallbackWaiting.Load(); w != nil {
		w.staged.Add(1)
		return
	}
	// The last executor left and its disconnect is still cancelling the
	// parked completions: the engine makes this daprd a fallback host only
	// after that, so the count waits for it.
	abe.fallbackStagedEarly.Add(1)
}

// fallbackWaiting warns on a fallback host while no daprd of the app has a
// workflow worker and the host has staged operations: a start, event or
// rerun does not advance until one exists.
type fallbackWaiting struct {
	clock clock.Clock
	// staged counts the operations staged since the daprd became a fallback
	// host. It is not decremented: a staged instance purged or terminated
	// since still counts.
	staged atomic.Int64
	cancel context.CancelFunc
	// warn logs the warning; a field so unit tests can capture it.
	warn func(staged int64, since time.Duration)
}

func newFallbackWaiting(appID string) *fallbackWaiting {
	return &fallbackWaiting{
		clock: clock.RealClock{},
		warn: func(staged int64, since time.Duration) {
			log.Warnf("This daprd has been a fallback host of app '%s' for %s, with no daprd of the app having a workflow worker; %d workflow operations it staged since run only once one connects",
				appID, since, staged)
		},
	}
}

// run warns at fallbackWaitingWarnAfter and at doubling intervals after it,
// once any operation has been staged, until ctx is done.
func (w *fallbackWaiting) run(ctx context.Context) {
	start := w.clock.Now()
	next := fallbackWaitingWarnAfter
	for {
		t := w.clock.NewTimer(start.Add(next).Sub(w.clock.Now()))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}

		if n := w.staged.Load(); n > 0 {
			w.warn(n, next)
		}
		next += min(next, fallbackWaitingWarnMaxInterval)
	}
}
