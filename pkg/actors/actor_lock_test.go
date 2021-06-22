// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var baseID = "test"

func TestLockBaseCase(t *testing.T) {
	for _, requestID := range []*string{&baseID, nil} {
		lock := NewActorLock(32)

		assert.Nil(t, lock.activeRequest)
		assert.Equal(t, int32(0), lock.stackDepth.Load())

		err := lock.Lock(requestID)

		assert.Nil(t, err)
		if requestID == nil {
			assert.Nil(t, lock.activeRequest)
		} else {
			assert.Equal(t, *requestID, *lock.activeRequest)
		}
		assert.Equal(t, int32(1), lock.stackDepth.Load())

		lock.Unlock()

		assert.Nil(t, lock.activeRequest)
		assert.Equal(t, int32(0), lock.stackDepth.Load())
	}
}

func TestLockBypassWithMatchingID(t *testing.T) {
	lock := NewActorLock(32)
	requestID := &baseID

	for i := 1; i < 5; i++ {
		err := lock.Lock(requestID)

		assert.Nil(t, err)
		assert.Equal(t, *requestID, *lock.activeRequest)
		assert.Equal(t, int32(i), lock.stackDepth.Load())
	}
}

func TestLockHoldsUntilStackIsZero(t *testing.T) {
	lock := NewActorLock(32)
	requestID := &baseID

	// Lock it twice.
	lock.Lock(requestID)
	lock.Lock(requestID)

	assert.Equal(t, *requestID, *lock.activeRequest)
	assert.Equal(t, int32(2), lock.stackDepth.Load())

	// Unlock until request is nil
	lock.Unlock()
	assert.Equal(t, *requestID, *lock.activeRequest)
	assert.Equal(t, int32(1), lock.stackDepth.Load())

	lock.Unlock()
	assert.Nil(t, lock.activeRequest)
	assert.Equal(t, int32(0), lock.stackDepth.Load())
}

func TestStackDepthLimit(t *testing.T) {
	lock := NewActorLock(1)
	requestID := &baseID

	err := lock.Lock(requestID)

	assert.Nil(t, err)
	assert.Equal(t, *requestID, *lock.activeRequest)
	assert.Equal(t, int32(1), lock.stackDepth.Load())

	err = lock.Lock(requestID)

	assert.NotNil(t, err)
	assert.Equal(t, "Maximum stack depth exceeded", err.Error())
}

func TestLockBlocksForNonMatchingID(t *testing.T) {
	lock := NewActorLock(32)
	firstRequestID := "first"
	secondRequestID := "second"

	firstInChan := make(chan int)
	firstOutChan := make(chan int)
	secondInChan := make(chan int)
	secondOutChan := make(chan int)

	go func() {
		<-firstInChan
		lock.Lock(&firstRequestID)
		firstOutChan <- 1
		<-firstInChan
		lock.Unlock()
		firstOutChan <- 1
	}()

	go func() {
		<-secondInChan
		lock.Lock(&secondRequestID)
		secondOutChan <- 2
		<-secondInChan
		lock.Unlock()
		secondOutChan <- 2
	}()

	assert.Nil(t, lock.activeRequest)
	assert.Equal(t, int32(0), lock.stackDepth.Load())

	firstInChan <- 1
	<-firstOutChan

	assert.Equal(t, firstRequestID, *lock.activeRequest)
	assert.Equal(t, int32(1), lock.stackDepth.Load())

	secondInChan <- 2

	assert.Equal(t, firstRequestID, *lock.activeRequest)
	assert.Equal(t, int32(1), lock.stackDepth.Load())

	firstInChan <- 1
	<-firstOutChan
	<-secondOutChan

	assert.Equal(t, secondRequestID, *lock.activeRequest)
	assert.Equal(t, int32(1), lock.stackDepth.Load())

	secondInChan <- 2
	<-secondOutChan

	assert.Nil(t, lock.activeRequest)
	assert.Nil(t, lock.activeRequest)
	assert.Equal(t, int32(0), lock.stackDepth.Load())
}
