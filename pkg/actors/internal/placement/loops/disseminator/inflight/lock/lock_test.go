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

package lock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/ptr"
)

type runningLock struct {
	loop.Interface[Event]
	done chan struct{}
}

func startLock(t *testing.T) *runningLock {
	t.Helper()

	l := &runningLock{Interface: New(), done: make(chan struct{})}
	go func() {
		defer close(l.done)
		assert.NoError(t, l.Run(t.Context()))
	}()
	return l
}

func (l *runningLock) close(t *testing.T, c *CloseLock) {
	t.Helper()

	l.Close(c)
	select {
	case <-l.done:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "lock loop did not stop")
	}
}

func acquire(t *testing.T, l loop.Interface[Event], ctx context.Context, actorType string) *Claim {
	t.Helper()

	respCh := make(chan *Claim, 1)
	l.Enqueue(&Acquire{
		ActorType: actorType,
		Context:   ctx,
		RespCh:    respCh,
	})
	select {
	case c := <-respCh:
		require.NotNil(t, c)
		return c
	case <-time.After(5 * time.Second):
		require.FailNow(t, "acquire did not resolve")
		return nil
	}
}

func requireDone(t *testing.T, ctx context.Context, msgAndArgs ...any) {
	t.Helper()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "claim context was not cancelled", msgAndArgs...)
	}
}

func TestClaimCancel_ConcurrentWithClose(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("placement stream closed")

	cases := map[string]*CloseLock{
		"no drain, immediate cancel": {
			Error:                 closeErr,
			DrainRebalancedActors: ptr.Of(false),
		},
		"drain timed out, force cancel": {
			Error:   closeErr,
			Timeout: ptr.Of(time.Nanosecond),
		},
	}

	for name, closeLock := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for i := range 300 {
				l := startLock(t)
				claims := []*Claim{
					acquire(t, l, t.Context(), "a"),
					acquire(t, l, t.Context(), "a"),
					acquire(t, l, t.Context(), "b"),
				}

				var wg sync.WaitGroup
				start := make(chan struct{})
				for _, c := range claims {
					wg.Go(func() {
						<-start
						c.Cancel(nil)
					})
				}

				close(start)
				l.close(t, closeLock)
				wg.Wait()

				for _, c := range claims {
					requireDone(t, c.Context, "iteration %d", i)
				}
			}
		})
	}
}

func TestClaimCancel_ConcurrentWithCancelTypes(t *testing.T) {
	t.Parallel()

	cancelErr := errors.New("placement table updated")

	cases := map[string]func() *CancelTypes{
		"no drain, immediate cancel": func() *CancelTypes {
			return &CancelTypes{
				Types:                 map[string]struct{}{"a": {}, "b": {}},
				Error:                 cancelErr,
				DrainRebalancedActors: ptr.Of(false),
				Done:                  make(chan struct{}),
			}
		},
		"drain timed out, force cancel": func() *CancelTypes {
			return &CancelTypes{
				Types:   map[string]struct{}{"a": {}, "b": {}},
				Error:   cancelErr,
				Timeout: ptr.Of(time.Nanosecond),
				Done:    make(chan struct{}),
			}
		},
	}

	for name, newEvent := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for i := range 300 {
				l := startLock(t)
				claims := []*Claim{
					acquire(t, l, t.Context(), "a"),
					acquire(t, l, t.Context(), "a"),
					acquire(t, l, t.Context(), "b"),
				}

				var wg sync.WaitGroup
				start := make(chan struct{})
				for _, c := range claims {
					wg.Go(func() {
						<-start
						c.Cancel(nil)
					})
				}

				ev := newEvent()
				close(start)
				l.Enqueue(ev)
				<-ev.Done
				wg.Wait()

				for _, c := range claims {
					requireDone(t, c.Context, "iteration %d", i)
				}

				l.close(t, &CloseLock{Timeout: ptr.Of(time.Second)})
			}
		})
	}
}

func TestClaimCancel_FirstCallWins(t *testing.T) {
	t.Parallel()

	l := startLock(t)
	c := acquire(t, l, t.Context(), "a")

	first := errors.New("first")
	c.Cancel(first)
	c.Cancel(errors.New("second"))

	requireDone(t, c.Context)
	require.ErrorIs(t, context.Cause(c.Context), first)

	start := time.Now()
	l.close(t, &CloseLock{Timeout: ptr.Of(10 * time.Second)})
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestClaimCancel_ConcurrentCallers(t *testing.T) {
	t.Parallel()

	for i := range 100 {
		l := startLock(t)
		c := acquire(t, l, t.Context(), "a")

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 8 {
			wg.Go(func() {
				<-start
				c.Cancel(nil)
			})
		}
		close(start)
		wg.Wait()

		requireDone(t, c.Context, "iteration %d", i)
		l.close(t, &CloseLock{Timeout: ptr.Of(10 * time.Second)})
	}
}

func TestClose_DrainsThenForceCancels(t *testing.T) {
	t.Parallel()

	l := startLock(t)

	finishedCtx, finish := context.WithCancel(t.Context())
	finished := acquire(t, l, finishedCtx, "a")
	held := acquire(t, l, t.Context(), "a")
	finish()

	closeErr := errors.New("placement stream closed")
	l.close(t, &CloseLock{Error: closeErr, Timeout: ptr.Of(50 * time.Millisecond)})

	requireDone(t, finished.Context)
	require.ErrorIs(t, context.Cause(finished.Context), context.Canceled)
	requireDone(t, held.Context)
	require.ErrorIs(t, context.Cause(held.Context), closeErr)
}

func TestClaimCancel_AfterClose(t *testing.T) {
	t.Parallel()

	for i := range 100 {
		parent, cancelParent := context.WithCancel(t.Context())

		l1 := startLock(t)
		stale := acquire(t, l1, parent, "a")
		cancelParent()
		l1.close(t, &CloseLock{Timeout: ptr.Of(time.Second)})

		l2 := startLock(t)

		done := make(chan struct{})
		go func() {
			defer close(done)
			stale.Cancel(errors.New("late cancel"))
		}()

		live := acquire(t, l2, t.Context(), "a")

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "late Cancel blocked", "iteration %d", i)
		}

		closeErr := errors.New("second session closed")
		l2.close(t, &CloseLock{Error: closeErr, Timeout: ptr.Of(10 * time.Millisecond)})
		requireDone(t, live.Context, "iteration %d", i)
		require.ErrorIs(t, context.Cause(live.Context), closeErr, "iteration %d", i)
	}
}
