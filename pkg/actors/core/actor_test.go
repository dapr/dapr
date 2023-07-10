/*
Copyright 2021 The Dapr Authors
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

package core

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"
)

var reentrancyStackDepth = 32

func TestIsBusy(t *testing.T) {
	testActor := newActor("testType", "testID", &reentrancyStackDepth, nil)

	testActor.Lock(nil)
	assert.Equal(t, true, testActor.IsBusy())
	testActor.Unlock()
}

func TestTurnBasedConcurrencyLocks(t *testing.T) {
	testActor := newActor("testType", "testID", &reentrancyStackDepth, nil)

	// first lock
	testActor.Lock(nil)
	assert.Equal(t, true, testActor.IsBusy())
	firstLockTime := testActor.LastUsedTime

	waitCh := make(chan bool)

	// second lock
	go func() {
		waitCh <- false
		testActor.Lock(nil)
		time.Sleep(10 * time.Millisecond)
		testActor.Unlock()
		waitCh <- false
	}()

	<-waitCh

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(2), testActor.pendingActorCalls.Load())
	assert.True(t, testActor.IsBusy())
	assert.Equal(t, firstLockTime, testActor.LastUsedTime)

	// unlock the first lock
	testActor.Unlock()

	assert.Equal(t, int32(1), testActor.pendingActorCalls.Load())
	assert.True(t, testActor.IsBusy())

	// unlock the second lock
	<-waitCh

	assert.Equal(t, int32(0), testActor.pendingActorCalls.Load())
	assert.False(t, testActor.IsBusy())
	assert.True(t, testActor.LastUsedTime.Sub(firstLockTime) >= 10*time.Millisecond)
}

func TestDisposedActor(t *testing.T) {
	t.Run("not disposed", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth, nil)

		testActor.Lock(nil)
		testActor.Unlock()
		testActor.disposeLock.RLock()
		disposed := testActor.disposed
		testActor.disposeLock.RUnlock()
		assert.False(t, disposed)
	})

	t.Run("disposed", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth, nil)

		testActor.Lock(nil)
		ch := testActor.Channel()
		assert.NotNil(t, ch)
		testActor.Unlock()

		err := testActor.Lock(nil)

		assert.Equal(t, int32(0), testActor.pendingActorCalls.Load())
		assert.IsType(t, ErrActorDisposed, err)
	})
}

func TestPendingActorCalls(t *testing.T) {
	t.Run("no pending actor call with new actor object", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth, nil)
		channelClosed := false

		select {
		case <-time.After(10 * time.Millisecond):
			break
		case <-testActor.Channel():
			channelClosed = true
			break
		}

		assert.False(t, channelClosed)
	})

	t.Run("close channel before timeout", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth, nil)
		testActor.Lock(nil)

		channelClosed := atomic.Bool{}
		go func() {
			select {
			case <-time.After(200 * time.Millisecond):
				require.Fail(t, "channel should be closed before timeout")
			case <-testActor.Channel():
				channelClosed.Store(true)
				break
			}
		}()

		time.Sleep(50 * time.Millisecond)
		testActor.Unlock()

		assert.Eventually(t, channelClosed.Load, time.Second, 10*time.Microsecond)
	})

	t.Run("multiple listeners", func(t *testing.T) {
		clock := clocktesting.NewFakeClock(time.Now())
		testActor := newActor("testType", "testID", &reentrancyStackDepth, clock)
		testActor.Lock(nil)

		nListeners := 10
		releaseSignaled := make([]atomic.Bool, nListeners)

		for i := 0; i < nListeners; i++ {
			releaseCh := testActor.Channel()
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
		testActor.Unlock()

		assert.Eventually(t, func() bool {
			clock.Step(100 * time.Millisecond)
			for i := 0; i < nListeners; i++ {
				if !releaseSignaled[i].Load() {
					return false
				}
			}
			return true
		}, time.Second, time.Microsecond)
	})
}
