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

package actors

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
	testActor := newActor("testType", "testID", &reentrancyStackDepth, time.Second, nil)

	testActor.lock(nil)
	assert.True(t, testActor.isBusy())
	testActor.unlock()
}

func TestTurnBasedConcurrencyLocks(t *testing.T) {
	testActor := newActor("testType", "testID", &reentrancyStackDepth, time.Second, nil)

	// first lock
	testActor.lock(nil)
	assert.True(t, testActor.isBusy())
	firstIdleAt := *testActor.idleAt.Load()

	waitCh := make(chan bool)

	// second lock
	go func() {
		waitCh <- false
		testActor.lock(nil)
		time.Sleep(10 * time.Millisecond)
		testActor.unlock()
		waitCh <- false
	}()

	<-waitCh

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, int32(2), testActor.pendingActorCalls.Load())
	assert.True(t, testActor.isBusy())
	assert.Equal(t, firstIdleAt, *testActor.idleAt.Load())

	// unlock the first lock
	testActor.unlock()

	assert.Equal(t, int32(1), testActor.pendingActorCalls.Load())
	assert.True(t, testActor.isBusy())

	// unlock the second lock
	<-waitCh

	assert.Equal(t, int32(0), testActor.pendingActorCalls.Load())
	assert.False(t, testActor.isBusy())
	assert.GreaterOrEqual(t, testActor.idleAt.Load().Sub(firstIdleAt), 10*time.Millisecond)
}

func TestDisposedActor(t *testing.T) {
	t.Run("not disposed", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth, time.Second, nil)

		testActor.lock(nil)
		testActor.unlock()
		testActor.disposeLock.RLock()
		disposed := testActor.disposed
		testActor.disposeLock.RUnlock()
		assert.False(t, disposed)
	})

	t.Run("disposed", func(t *testing.T) {
		testActor := newActor("testType", "testID", &reentrancyStackDepth, time.Second, nil)

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
		testActor := newActor("testType", "testID", &reentrancyStackDepth, time.Second, nil)
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
		testActor := newActor("testType", "testID", &reentrancyStackDepth, time.Second, nil)
		testActor.lock(nil)

		channelClosed := atomic.Bool{}
		go func() {
			select {
			case <-time.After(200 * time.Millisecond):
				require.Fail(t, "channel should be closed before timeout")
			case <-testActor.channel():
				channelClosed.Store(true)
				break
			}
		}()

		time.Sleep(50 * time.Millisecond)
		testActor.unlock()

		assert.Eventually(t, channelClosed.Load, time.Second, 10*time.Microsecond)
	})

	t.Run("multiple listeners", func(t *testing.T) {
		clock := clocktesting.NewFakeClock(time.Now())
		testActor := newActor("testType", "testID", &reentrancyStackDepth, time.Second, clock)
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
