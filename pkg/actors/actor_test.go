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
	testActor.unLock()
}

func TestConcurrencyLocks(t *testing.T) {
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
	assert.True(t, testActor.isBusy())
	assert.Equal(t, firstLockTime, testActor.lastUsedTime)

	// unlock the first lock
	testActor.unLock()

	time.Sleep(10 * time.Millisecond)
	assert.True(t, testActor.isBusy())
	assert.NotEqual(t, firstLockTime, testActor.lastUsedTime)

	// unlock the second lock
	testActor.unLock()
	assert.False(t, testActor.isBusy())
	assert.NotEqual(t, firstLockTime, testActor.lastUsedTime)
}

func TestBusyChannel(t *testing.T) {
	testActor := newActor("testType", "testID")
	testActor.lock()

	var channelClosed = false
	go func() {
		select {
		case <-time.After(10 * time.Second):
			break
		case <-testActor.channel():
			channelClosed = true
			break
		}
	}()

	time.Sleep(10 * time.Millisecond)
	testActor.unLock()
	time.Sleep(100 * time.Millisecond)
	assert.True(t, channelClosed)
}
