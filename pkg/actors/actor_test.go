// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
)

var reentrancyStackDepth = 32

func TestIsBusy(t *testing.T) {
	testActor := newActor("testType", "testID", &reentrancyStackDepth)

	testActor.lock(nil)
	assert.Equal(t, true, testActor.isBusy())
	testActor.unlock()
}

func TestTurnBasedConcurrencyLocks(t *testing.T) {
	testActor := newActor("testType", "testID", &reentrancyStackDepth)

	// first lock
	testActor.lock(nil)
	assert.Equal(t, true, testActor.isBusy())
	firstLockTime := testActor.lastUsedTime

	waitCh := make(chan bool)

	// second lock
	go func() {
		waitCh <- false
		testActor.lock(nil)
		time.Sleep(100 * time.Millisecond)
		testActor.unlock()
		waitCh <- false
	}()

	<-waitCh

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(2), testActor.pendingActorCalls.Load())
	assert.True(t, testActor.isBusy())
	assert.Equal(t, firstLockTime, testActor.lastUsedTime)

	// unlock the first lock
	testActor.unlock()

	assert.Equal(t, int32(1), testActor.pendingActorCalls.Load())
	assert.True(t, testActor.isBusy())

	// unlock the second lock
	<-waitCh
	assert.Equal(t, int32(0), testActor.pendingActorCalls.Load())
	assert.False(t, testActor.isBusy())
	assert.True(t, testActor.lastUsedTime.Sub(firstLockTime) >= 10*time.Millisecond)
}

func TestDisposedActor(t *testing.T) {
	t.Run("not disposed", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth)

		testActor.lock(nil)
		testActor.unlock()
		testActor.disposeLock.RLock()
		disposed := testActor.disposed
		testActor.disposeLock.RUnlock()
		assert.False(t, disposed)
	})

	t.Run("disposed", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth)

		testActor.lock(nil)
		ch := testActor.channel()
		assert.NotNil(t, ch)
		testActor.unlock()

		err := testActor.lock(nil)

		assert.Equal(t, int32(0), testActor.pendingActorCalls.Load())
		assert.IsType(t, ErrActorDisposed, err)
	})
}

func TestPendingActorCalls(t *testing.T) {
	t.Run("no pending actor call with new actor object", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth)
		channelClosed := false

		select {
		case <-time.After(10 * time.Millisecond):
			break
		case <-testActor.channel():
			channelClosed = true
			break
		}

		assert.False(t, channelClosed)
	})

	t.Run("close channel before timeout", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth)
		testActor.lock(nil)

		channelClosed := atomic.NewBool(false)
		go func() {
			select {
			case <-time.After(200 * time.Millisecond):
				break
			case <-testActor.channel():
				channelClosed.Store(true)
				break
			}
		}()

		time.Sleep(10 * time.Millisecond)
		testActor.unlock()
		time.Sleep(100 * time.Millisecond)
		assert.True(t, channelClosed.Load())
	})

	t.Run("multiple listeners", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth)
		testActor.lock(nil)

		nListeners := 10
		releaseSignaled := make([]atomic.Bool, nListeners)

		for i := 0; i < nListeners; i++ {
			releaseCh := testActor.channel()
			go func(listenerIndex int) {
				select {
				case <-time.After(200 * time.Millisecond):
					break
				case <-releaseCh:
					releaseSignaled[listenerIndex].Store(true)
					break
				}
			}(i)
		}
		testActor.unlock()
		time.Sleep(100 * time.Millisecond)

		for i := 0; i < nListeners; i++ {
			assert.True(t, releaseSignaled[i].Load())
		}
	})
}
