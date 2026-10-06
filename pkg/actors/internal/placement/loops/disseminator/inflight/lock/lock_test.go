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
)

// testHandler runs func() events on the loop goroutine, so a test can read
// the lock state without racing the loop. It passes every other event to
// the lock.
type testHandler struct{ l *lock }

func (h testHandler) Handle(ctx context.Context, event Event) error {
	if fn, ok := event.(func()); ok {
		fn()
		return nil
	}
	return h.l.Handle(ctx, event)
}

func newTestLock(t *testing.T) *lock {
	t.Helper()

	l := &lock{acquires: make(map[uint64]*Claim)}
	l.loop = LoopFactory.NewLoop(testHandler{l: l})

	errCh := make(chan error, 1)
	go func() { errCh <- l.loop.Run(context.Background()) }()
	t.Cleanup(func() {
		drain := false
		l.loop.Close(&CloseLock{DrainRebalancedActors: &drain})
		require.NoError(t, <-errCh)
	})

	return l
}

func acquire(t *testing.T, l *lock, ctx context.Context) *Claim {
	t.Helper()

	respCh := make(chan *Claim, 1)
	l.loop.Enqueue(&Acquire{ActorType: "a", Context: ctx, RespCh: respCh})
	select {
	case claim := <-respCh:
		return claim
	case <-time.After(time.Second * 5):
		require.Fail(t, "timed out waiting for the claim")
		return nil
	}
}

func acquiresLen(l *lock) int {
	ch := make(chan int, 1)
	l.loop.Enqueue(func() { ch <- len(l.acquires) })
	return <-ch
}

func TestClaimCancelConcurrent(t *testing.T) {
	l := newTestLock(t)
	claim := acquire(t, l, context.Background())
	require.Equal(t, 1, acquiresLen(l))

	// The drain goroutine and the request goroutine can call Cancel at the
	// same time. Run with -race.
	errA := errors.New("drain timed out")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { claim.Cancel(errA) })
	}
	wg.Wait()

	require.ErrorIs(t, context.Cause(claim.Context), errA)
	assert.Eventually(t, func() bool { return acquiresLen(l) == 0 }, time.Second*5, time.Millisecond*10)
}

func TestClaimReleasedWhenCallerContextEnds(t *testing.T) {
	l := newTestLock(t)

	ctx, cancel := context.WithCancel(context.Background())
	claim := acquire(t, l, ctx)
	require.Equal(t, 1, acquiresLen(l))

	// The caller stops without a call to Cancel.
	cancel()

	require.Error(t, claim.Context.Err())
	assert.Eventually(t, func() bool { return acquiresLen(l) == 0 }, time.Second*5, time.Millisecond*10)

	// A late Cancel is safe.
	claim.Cancel(errors.New("late"))
	assert.Equal(t, 0, acquiresLen(l))
}

func TestClaimOnDoneContextIsReleased(t *testing.T) {
	l := newTestLock(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	claim := acquire(t, l, ctx)

	require.Error(t, claim.Context.Err())
	assert.Eventually(t, func() bool { return acquiresLen(l) == 0 }, time.Second*5, time.Millisecond*10)
}

func TestClaimCancelDoesNotReleaseUntilCalled(t *testing.T) {
	l := newTestLock(t)
	claim := acquire(t, l, context.Background())

	// Without a Cancel and with a live caller context, the claim stays.
	time.Sleep(time.Millisecond * 100)
	require.Equal(t, 1, acquiresLen(l))
	require.NoError(t, claim.Context.Err())

	claim.Cancel(nil)
	assert.Eventually(t, func() bool { return acquiresLen(l) == 0 }, time.Second*5, time.Millisecond*10)
}

func TestReleaseIgnoresOtherClaimWithSameIdx(t *testing.T) {
	// A release from an earlier lock lifetime can reach a recycled lock whose
	// claim has the same idx. It must not remove that claim.
	live := &Claim{ActorType: "a"}
	l := &lock{acquires: map[uint64]*Claim{0: live}}

	l.handleRelease(&releaseClaim{idx: 0, claim: &Claim{ActorType: "a"}})
	require.Len(t, l.acquires, 1)

	l.handleRelease(&releaseClaim{idx: 0, claim: live})
	require.Empty(t, l.acquires)
}
