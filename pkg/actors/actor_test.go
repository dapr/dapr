// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsBusy(t *testing.T) {
	testActor := newActor("testType", "testID")

	testActor.lock()
	assert.Equal(t, true, testActor.isBusy())
	testActor.unlock()
}

func TestTurnBasedConcurrencyLocks(t *testing.T) {
	testActor := newActor("testType", "testID")

	// first lock
	testActor.lock()
	assert.Equal(t, true, testActor.isBusy())
	firstLockTime := testActor.lastUsedTime

	waitCh := make(chan bool)

	// second lock
	go func() {
		waitCh <- false
		testActor.lock()
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
		testActor := newActor("testType", "testID")

		testActor.lock()
		testActor.unlock()
		assert.False(t, testActor.disposed)
	})

	t.Run("disposed", func(t *testing.T) {
		testActor := newActor("testType", "testID")

		testActor.lock()
		ch := testActor.channel()
		assert.NotNil(t, ch)
		testActor.unlock()

		err := testActor.lock()

		assert.Equal(t, int32(0), testActor.pendingActorCalls.Load())
		assert.IsType(t, ErrActorDisposed, err)
	})
}

func TestPendingActorCalls(t *testing.T) {
	t.Run("no pending actor call with new actor object", func(t *testing.T) {
		testActor := newActor("testType", "testID")
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
		testActor := newActor("testType", "testID")
		testActor.lock()

		channelClosed := false
		go func() {
			select {
			case <-time.After(200 * time.Millisecond):
				break
			case <-testActor.channel():
				channelClosed = true
				break
			}
		}()

		time.Sleep(10 * time.Millisecond)
		testActor.unlock()
		time.Sleep(100 * time.Millisecond)
		assert.True(t, channelClosed)
	})

	t.Run("multiple listeners", func(t *testing.T) {
		testActor := newActor("testType", "testID")
		testActor.lock()

		nListeners := 10
		releaseSignaled := make([]bool, nListeners)

		for i := 0; i < nListeners; i++ {
			releaseCh := testActor.channel()
			go func(listenerIndex int) {
				select {
				case <-time.After(200 * time.Millisecond):
					break
				case <-releaseCh:
					releaseSignaled[listenerIndex] = true
					break
				}
			}(i)
		}
		testActor.unlock()
		time.Sleep(100 * time.Millisecond)

		for i := 0; i < nListeners; i++ {
			assert.True(t, releaseSignaled[i])
		}
	})
}
