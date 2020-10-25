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

	// second lock
	go func() {
		testActor.lock()
	}()

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(2), testActor.pendingActorCalls)
	assert.True(t, testActor.isBusy())
	assert.Equal(t, firstLockTime, testActor.lastUsedTime)

	// unlock the first lock
	testActor.unlock()

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(1), testActor.pendingActorCalls)
	assert.True(t, testActor.isBusy())
	assert.NotEqual(t, firstLockTime, testActor.lastUsedTime)

	// unlock the second lock
	testActor.unlock()
	assert.Equal(t, int32(0), testActor.pendingActorCalls)
	assert.False(t, testActor.isBusy())
	assert.NotEqual(t, firstLockTime, testActor.lastUsedTime)
}

func TestBusyChannel(t *testing.T) {
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
