/*
Copyright 2022 The Dapr Authors
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

package manager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	kclock "k8s.io/utils/clock"
	testingclock "k8s.io/utils/clock/testing"
)

func TestConnectionPoolConnection(t *testing.T) {
	// Allow mocking time
	clockMock := testingclock.NewFakeClock(time.Now())
	clock = clockMock
	defer func() {
		// Reset time
		clock = &kclock.RealClock{}
	}()

	cpc := &connectionPoolConnection{}
	maxConnIdle := 10 * time.Second

	t.Run("new connectionPoolConnection is never expired", func(t *testing.T) {
		assert.False(t, cpc.Expired(maxConnIdle))
	})
	t.Run("test connection expiring", func(t *testing.T) {
		// Mark the connection as idle now
		cpc.MarkIdle()

		// Increase time by 1s, 10 times
		for i := 0; i < 10; i++ {
			assert.False(t, cpc.Expired(maxConnIdle))
			clockMock.Step(time.Second)
		}

		// After 11s, should be expired
		assert.False(t, cpc.Expired(maxConnIdle))
		clockMock.Step(time.Second)
		assert.True(t, cpc.Expired(maxConnIdle))
	})
}

func TestConnectionPool(t *testing.T) {
	// Allow mocking time
	clockMock := testingclock.NewFakeClock(time.Now())
	clock = clockMock
	defer func() {
		// Reset time
		clock = &kclock.RealClock{}
	}()

	cp := NewConnectionPool(10*time.Second, 0)
	require.NotNil(t, cp)

	conns := []*mockConnection{
		{},
		{},
	}

	t.Run("share returns nil with no active connection", func(t *testing.T) {
		conn := cp.Share()
		assert.Nil(t, conn)
	})

	t.Run("one connection: register, share, release", func(t *testing.T) {
		// Register the connection
		cp.Register(conns[0])
		require.Len(t, cp.connections, 1)
		require.Equal(t, int32(0), cp.connections[0].referenceCount)

		// Share should return the connection that was just registered and increase the reference count
		conn := cp.Share()
		require.Equal(t, conns[0], conn)
		require.Equal(t, int32(1), cp.connections[0].referenceCount)
		require.False(t, conns[0].Closed)

		// Release should decrease the reference count but not close the connection
		// Should also set idleSince to the current time
		cp.Release(conn)
		require.Equal(t, int32(0), cp.connections[0].referenceCount)
		require.False(t, conns[0].Closed)
		require.NotNil(t, cp.connections[0].idleSince.Load())
		require.Equal(t, clock.Now(), *(cp.connections[0].idleSince.Load()))
	})

	t.Run("two connections", func(t *testing.T) {
		var conn grpc.ClientConnInterface

		// Register the second connection
		cp.Register(conns[1])
		require.Len(t, cp.connections, 2)
		require.Equal(t, int32(0), cp.connections[0].referenceCount)
		require.Equal(t, int32(0), cp.connections[1].referenceCount)

		// Share the connection grpcMaxConcurrentStreams times (100)
		// Should always return the first connection
		for i := 0; i < grpcMaxConcurrentStreams; i++ {
			conn = cp.Share()
			require.Equal(t, conns[0], conn)
			require.Equal(t, int32(i+1), cp.connections[0].referenceCount)
			require.Equal(t, int32(0), cp.connections[1].referenceCount)
		}

		require.Equal(t, int32(100), cp.connections[0].referenceCount)

		// Next grpcMaxConcurrentStreams should return the second connection
		for i := 0; i < grpcMaxConcurrentStreams; i++ {
			conn = cp.Share()
			require.Equal(t, conns[1], conn)
			require.Equal(t, int32(i+1), cp.connections[1].referenceCount)
			require.Equal(t, int32(100), cp.connections[0].referenceCount)
		}

		// Next call to Share should return nil because all connections are at capacity
		conn = cp.Share()
		assert.Nil(t, conn)

		// Release one from each connection
		// When both connections have capacity, the first one is used first
		cp.Release(conns[0])
		cp.Release(conns[1])
		require.Equal(t, int32(99), cp.connections[0].referenceCount)
		require.Equal(t, int32(99), cp.connections[1].referenceCount)

		conn = cp.Share()
		require.Equal(t, conns[0], conn)
		require.Equal(t, int32(100), cp.connections[0].referenceCount)
		require.Equal(t, int32(99), cp.connections[1].referenceCount)

		conn = cp.Share()
		require.Equal(t, conns[1], conn)
		require.Equal(t, int32(100), cp.connections[0].referenceCount)
		require.Equal(t, int32(100), cp.connections[1].referenceCount)

		// Destroy all connections
		cp.DestroyAll()
		require.Empty(t, cp.connections)
		require.True(t, conns[0].Closed)
		require.True(t, conns[1].Closed)
	})

	conns[0].Closed = false
	conns[1].Closed = false

	t.Run("test get method", func(t *testing.T) {
		// Set connection fn that returns from the pre-created conns
		n := 0
		errNoNewConns := errors.New("no new connection available")
		createFn := func() (grpc.ClientConnInterface, error) {
			if n >= len(conns) {
				return nil, errNoNewConns
			}
			c := conns[n]
			n++
			return c, nil
		}

		// First call should invoke the createFn and return the first function
		conn, err := cp.Get(createFn)
		require.NoError(t, err)
		require.Equal(t, conns[0], conn)
		require.Equal(t, 1, n)

		// Next grpcMaxConcurrentStreams-1 calls (99) should still return the first connection
		for i := 1; i < grpcMaxConcurrentStreams; i++ { // Start from 1
			conn, err = cp.Get(createFn)
			require.NoError(t, err)
			require.Equal(t, conns[0], conn)
			require.Equal(t, 1, n) // Should not have called createFn again
			require.Equal(t, int32(i+1), cp.connections[0].referenceCount)
		}

		// Next grpcMaxConcurrentStreams calls should return the second connection
		// After having invoked the method one more time
		for i := 0; i < grpcMaxConcurrentStreams; i++ {
			conn, err = cp.Get(createFn)
			require.NoError(t, err)
			require.Equal(t, conns[1], conn)
			require.Equal(t, 2, n) // Should have called the function 2 times in total
			require.Equal(t, int32(i+1), cp.connections[1].referenceCount)
			require.Equal(t, int32(100), cp.connections[0].referenceCount)
		}

		// Next call should return errNoNewConns (which is just from our mock handler)
		conn, err = cp.Get(createFn)
		require.Equal(t, errNoNewConns, err)
		require.Nil(t, conn)

		// Release one from each connection
		cp.Release(conns[0])
		cp.Release(conns[1])
		require.Equal(t, int32(99), cp.connections[0].referenceCount)
		require.Equal(t, int32(99), cp.connections[1].referenceCount)

		// Next calls should return conns[0] and conns[1], without calling createFn again
		conn, err = cp.Get(createFn)
		require.NoError(t, err)
		require.Equal(t, conns[0], conn)

		conn, err = cp.Get(createFn)
		require.NoError(t, err)
		require.Equal(t, conns[1], conn)

		// Forcefully reset reference count
		cp.connections[0].referenceCount = 0
		cp.connections[1].referenceCount = 0

		// Destroy the first connection
		cp.Destroy(conns[0])

		require.Len(t, cp.connections, 1)
		require.Equal(t, conns[1], cp.connections[0].conn)

		// Calls to get should return the second connection
		conn, err = cp.Get(createFn)
		require.NoError(t, err)
		require.Equal(t, conns[1], conn)

		// Destroy all connections
		cp.DestroyAll()
		require.Empty(t, cp.connections)
		require.True(t, conns[0].Closed)
		require.True(t, conns[1].Closed)
	})

	testExpiredConn := func(minActiveConns int) func(t *testing.T) {
		// Reset the object
		cp.DestroyAll()
		require.Empty(t, cp.connections)
		conns[0].Closed = false
		conns[1].Closed = false
		cp.Register(conns[0])
		cp.Register(conns[1])
		cp.minActiveConns = minActiveConns

		return func(t *testing.T) {
			// Start by fully using all available connections (requires grpcMaxConcurrentStreams+1 calls)
			for i := 0; i < grpcMaxConcurrentStreams+1; i++ {
				conn := cp.Share()
				if i < grpcMaxConcurrentStreams {
					require.Equal(t, conns[0], conn)
				} else {
					require.Equal(t, conns[1], conn)
				}
			}

			require.Equal(t, int32(100), cp.connections[0].referenceCount)
			require.Equal(t, int32(1), cp.connections[1].referenceCount)

			// Calling purge should not remove any connection
			cp.Purge()

			require.Len(t, cp.connections, 2)
			require.Equal(t, int32(100), cp.connections[0].referenceCount)
			require.Equal(t, int32(1), cp.connections[1].referenceCount)

			// Wait 15 seconds (more than expiration time) and repeat
			// Because the second connection is still in use, it won't be purged
			clockMock.Step(15 * time.Second)

			require.Len(t, cp.connections, 2)
			require.Equal(t, int32(100), cp.connections[0].referenceCount)
			require.Equal(t, int32(1), cp.connections[1].referenceCount)

			// Release the second connection, which should now become idle
			cp.Release(conns[1])

			require.Equal(t, int32(100), cp.connections[0].referenceCount)
			require.Equal(t, int32(0), cp.connections[1].referenceCount)
			require.Equal(t, clock.Now(), *(cp.connections[1].idleSince.Load()))

			// Calling purge should not remove any connection
			cp.Purge()

			require.Len(t, cp.connections, 2)
			require.Equal(t, int32(100), cp.connections[0].referenceCount)
			require.Equal(t, int32(0), cp.connections[1].referenceCount)

			// Wait 15 seconds (more than expiration time)
			// Share should return nil because the first connection is full, and the second has expired
			clockMock.Step(15 * time.Second)
			conn := cp.Share()
			require.Nil(t, conn)
			require.Equal(t, int32(0), cp.connections[1].referenceCount)

			// Now the second connection should be purged
			cp.Purge()

			require.Len(t, cp.connections, 1)
			require.Equal(t, conns[0], cp.connections[0].conn)
			require.Equal(t, int32(100), cp.connections[0].referenceCount)

			// Release the first connection 100 times, to bring it to 0
			for i := 0; i < grpcMaxConcurrentStreams; i++ {
				cp.Release(conns[0])
			}
			idleStart := clock.Now()

			require.Len(t, cp.connections, 1)
			require.Equal(t, conns[0], cp.connections[0].conn)
			require.Equal(t, int32(0), cp.connections[0].referenceCount)
			require.Equal(t, idleStart, *(cp.connections[0].idleSince.Load()))

			// Calling purge should not remove any connection
			cp.Purge()

			require.Len(t, cp.connections, 1)
			require.Equal(t, int32(0), cp.connections[0].referenceCount)
			require.Equal(t, idleStart, *(cp.connections[0].idleSince.Load()))

			// Wait 15 seconds (more than expiration time)
			// Share should return nil because the connection has expired
			clockMock.Step(15 * time.Second)
			conn = cp.Share()
			require.Nil(t, conn)
			require.Equal(t, int32(0), cp.connections[0].referenceCount)

			// Now the first connection should be purged if minActiveConns == 0 only
			clockMock.Step(15 * time.Second)
			cp.Purge()

			if minActiveConns == 0 {
				require.Empty(t, cp.connections)
			} else {
				require.Len(t, cp.connections, 1)
				require.Equal(t, conns[0], cp.connections[0].conn)
				require.Equal(t, int32(0), cp.connections[0].referenceCount)
				require.Equal(t, idleStart, *(cp.connections[0].idleSince.Load()))
			}
		}
	}

	t.Run("expired connection", testExpiredConn(0))
	t.Run("expired connection with 1 min", testExpiredConn(1))
}

// Mock that implements grpc.ClientConnInterface and the Close method
type mockConnection struct {
	Closed bool
}

func (c *mockConnection) Close() error {
	c.Closed = true
	return nil
}

func (c *mockConnection) Invoke(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
	return nil
}

func (c *mockConnection) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, nil
}
