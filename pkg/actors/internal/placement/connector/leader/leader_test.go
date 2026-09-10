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

package leader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/runtime/scheduler/leadership"
)

func newTest(l *leadership.Leadership) *leader {
	return New(Options{
		Leadership:  l,
		GRPCOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
	}).(*leader)
}

func TestConnectUnsupported(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.SetUnsupported()

	_, err := newTest(ldr).Connect(t.Context())
	require.ErrorIs(t, err, ErrSchedulerPlacementUnsupported)
}

func TestConnectWaitsForLeader(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	conn := newTest(ldr)

	type result struct {
		conn *grpc.ClientConn
		err  error
	}
	resCh := make(chan result)
	go func() {
		c, err := conn.Connect(t.Context())
		resCh <- result{c, err}
	}()

	select {
	case res := <-resCh:
		require.Fail(t, "Connect must block while no leader is advertised", "got %v", res)
	case <-time.After(time.Millisecond * 100):
	}

	ldr.Set("127.0.0.1:1")

	select {
	case res := <-resCh:
		require.NoError(t, res.err)
		require.NotNil(t, res.conn)
		assert.Equal(t, "127.0.0.1:1", conn.Address())
		res.conn.Close()
	case <-time.After(time.Second * 5):
		require.Fail(t, "Connect must return once a leader is advertised")
	}
}

func TestConnectContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error)
	go func() {
		_, err := newTest(leadership.New()).Connect(ctx)
		errCh <- err
	}()

	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second * 5):
		require.Fail(t, "Connect must return on context cancellation")
	}
}

func TestWatchLeaderClosesMovedConnection(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.Set("127.0.0.1:1")
	conn, err := newTest(ldr).Connect(t.Context())
	require.NoError(t, err)

	ldr.Set("127.0.0.1:2")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, connectivity.Shutdown, conn.GetState())
	}, time.Second*5, time.Millisecond*10)
}

func TestWatchLeaderClosesOnUnsupported(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.Set("127.0.0.1:1")
	conn, err := newTest(ldr).Connect(t.Context())
	require.NoError(t, err)

	ldr.SetUnsupported()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, connectivity.Shutdown, conn.GetState())
	}, time.Second*5, time.Millisecond*10)
}

func TestConnectClosesPreviousConnection(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.Set("127.0.0.1:1")
	l := newTest(ldr)

	first, err := l.Connect(t.Context())
	require.NoError(t, err)

	second, err := l.Connect(t.Context())
	require.NoError(t, err)
	defer second.Close()

	assert.Equal(t, connectivity.Shutdown, first.GetState())
}
