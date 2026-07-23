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

package timeout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTimeout(t *testing.T, duration time.Duration) (*Timeout, chan uint64) {
	t.Helper()

	events := make(chan uint64, 1)
	timeout := New(Options{
		OnTimeout: func(version uint64) {
			events <- version
		},
		Timeout: duration,
	})
	t.Cleanup(func() {
		require.NoError(t, timeout.Close())
	})

	return timeout, events
}

func TestTimeoutEnqueue(t *testing.T) {
	timeout, events := newTimeout(t, 10*time.Millisecond)
	timeout.Enqueue(42)

	select {
	case version := <-events:
		assert.Equal(t, uint64(42), version)
	case <-time.After(time.Second):
		t.Fatal("timeout event was not enqueued")
	}
}

func TestTimeoutDequeue(t *testing.T) {
	timeout, events := newTimeout(t, time.Second)
	timeout.Enqueue(1)
	timeout.Dequeue(1)

	select {
	case version := <-events:
		t.Fatalf("unexpected timeout event for version %d", version)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTimeoutDequeueIgnoresAnotherVersion(t *testing.T) {
	timeout, events := newTimeout(t, 10*time.Millisecond)
	timeout.Enqueue(2)
	timeout.Dequeue(1)

	select {
	case version := <-events:
		assert.Equal(t, uint64(2), version)
	case <-time.After(time.Second):
		t.Fatal("active timeout was canceled by another version")
	}
}

func TestTimeoutRearmsAfterDequeue(t *testing.T) {
	timeout, events := newTimeout(t, time.Second)

	for version := uint64(1); version <= 100; version++ {
		timeout.Enqueue(version)
		timeout.Dequeue(version)
	}
	timeout.Enqueue(101)

	select {
	case version := <-events:
		assert.Equal(t, uint64(101), version)
	case <-time.After(2 * time.Second):
		t.Fatal("rearmed timeout event was not enqueued")
	}
}

func TestTimeoutEnqueueReplacesActiveVersion(t *testing.T) {
	timeout, events := newTimeout(t, time.Second)
	timeout.Enqueue(1)
	timeout.Enqueue(2)

	select {
	case version := <-events:
		assert.Equal(t, uint64(2), version)
	case <-time.After(2 * time.Second):
		t.Fatal("replacement timeout event was not enqueued")
	}

	select {
	case version := <-events:
		t.Fatalf("unexpected second timeout event for version %d", version)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTimeoutCloseCancelsActiveVersion(t *testing.T) {
	timeout, events := newTimeout(t, time.Second)
	timeout.Enqueue(1)
	require.NoError(t, timeout.Close())
	require.NoError(t, timeout.Close())
	timeout.Enqueue(2)

	select {
	case version := <-events:
		t.Fatalf("unexpected timeout event for version %d", version)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTimeoutCloseWaitsForFiredCallback(t *testing.T) {
	enqueueStarted := make(chan struct{})
	releaseEnqueue := make(chan struct{})
	timeout := New(Options{
		OnTimeout: func(uint64) {
			close(enqueueStarted)
			<-releaseEnqueue
		},
		Timeout: 10 * time.Millisecond,
	})
	timeout.Enqueue(1)

	select {
	case <-enqueueStarted:
	case <-time.After(time.Second):
		t.Fatal("timeout callback did not start enqueueing")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- timeout.Close()
	}()

	select {
	case err := <-closeDone:
		require.NoError(t, err)
		t.Fatal("Close returned before the fired callback completed")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseEnqueue)
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the fired callback completed")
	}
}
