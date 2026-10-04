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
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
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

func TestWatchLeaderKeepsConnectionOnLeaderlessBroadcast(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.Set("127.0.0.1:1")
	conn, err := newTest(ldr).Connect(t.Context())
	require.NoError(t, err)

	ldr.Set("")
	require.Never(t, func() bool {
		return conn.GetState() == connectivity.Shutdown
	}, time.Millisecond*400, time.Millisecond*50)

	ldr.Set("127.0.0.1:2")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, connectivity.Shutdown, conn.GetState())
	}, time.Second*5, time.Millisecond*10)
}

// TestWatcherSurvivesBoundedConnectContext covers the caller contract: the
// context given to Connect carries the leader watcher, so a bound lifted
// after a successful connect must not have cancelled it.
func TestWatcherSurvivesBoundedConnectContext(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.Set("127.0.0.1:1")
	l := newTest(ldr)

	cctx, cancel := context.WithCancelCause(t.Context())
	conn, err := l.Connect(cctx)
	require.NoError(t, err)

	require.Never(t, func() bool {
		return conn.GetState() == connectivity.Shutdown
	}, time.Millisecond*400, time.Millisecond*50)

	ldr.Set("127.0.0.1:2")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, connectivity.Shutdown, conn.GetState())
	}, time.Second*5, time.Millisecond*10)
	cancel(nil)
}

// TestWatcherRestartsAfterConnectContextCancelled covers a startup connect
// whose bounded context is cancelled after the watcher started: the next
// Connect must start a fresh watcher which still follows leader moves.
func TestWatcherRestartsAfterConnectContextCancelled(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.Set("127.0.0.1:1")
	l := newTest(ldr)

	cctx, cancel := context.WithCancel(t.Context())
	first, err := l.Connect(cctx)
	require.NoError(t, err)
	cancel()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, connectivity.Shutdown, first.GetState())
	}, time.Second*5, time.Millisecond*10)

	second, err := l.Connect(t.Context())
	require.NoError(t, err)

	ldr.Set("127.0.0.1:2")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, connectivity.Shutdown, second.GetState())
	}, time.Second*5, time.Millisecond*10)
}

// TestWatcherClosesOnlyItsOwnConnection covers a cancelled watcher racing a
// newer Connect: the cleanup must not close a connection it was not started
// for.
func TestWatcherClosesOnlyItsOwnConnection(t *testing.T) {
	t.Parallel()

	ldr := leadership.New()
	ldr.Set("127.0.0.1:1")
	l := newTest(ldr)

	newConn := func() *grpc.ClientConn {
		conn, err := grpc.NewClient("127.0.0.1:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	connA, connB := newConn(), newConn()

	l.lock.Lock()
	l.conn = connB
	l.addr = "127.0.0.1:1"
	l.watchStarted = true
	l.lock.Unlock()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	l.watchLeader(ctx, connA)

	l.lock.Lock()
	defer l.lock.Unlock()
	assert.Same(t, connB, l.conn, "a newer connection must survive the old watcher's cleanup")
	assert.False(t, l.watchStarted)
	assert.NotEqual(t, connectivity.Shutdown, connB.GetState())
}

// blackholeListener simulates a network partition. Once enabled, connections
// stay open but everything sent either way is silently discarded.
type blackholeListener struct {
	net.Listener
	enabled atomic.Bool
}

func (b *blackholeListener) Accept() (net.Conn, error) {
	conn, err := b.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &blackholeConn{Conn: conn, enabled: &b.enabled}, nil
}

type blackholeConn struct {
	net.Conn
	enabled *atomic.Bool
}

func (c *blackholeConn) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if err != nil || !c.enabled.Load() {
			return n, err
		}
	}
}

func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.enabled.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

type reportServer struct {
	schedulerv1pb.UnimplementedSchedulerServer
	received chan struct{}
}

func (r *reportServer) ReportActorTypes(stream schedulerv1pb.Scheduler_ReportActorTypesServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	close(r.received)
	<-stream.Context().Done()
	return nil
}

func TestConnectDetectsPartitionedLeader(t *testing.T) {
	t.Parallel()

	port, err := freeport.GetFreePort()
	require.NoError(t, err)
	lis, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err)
	bh := &blackholeListener{Listener: lis}

	srv := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		MinTime:             time.Second * 5,
		PermitWithoutStream: true,
	}))
	rs := &reportServer{received: make(chan struct{})}
	schedulerv1pb.RegisterSchedulerServer(srv, rs)
	go srv.Serve(bh)
	t.Cleanup(srv.Stop)

	ldr := leadership.New()
	ldr.Set(lis.Addr().String())
	conn, err := newTest(ldr).Connect(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	stream, err := schedulerv1pb.NewSchedulerClient(conn).ReportActorTypes(t.Context())
	require.NoError(t, err)
	require.NoError(t, stream.Send(new(schedulerv1pb.ReportActorTypesRequest)))
	select {
	case <-rs.received:
	case <-time.After(time.Second * 5):
		require.Fail(t, "placement stream never reached the leader")
	}

	bh.enabled.Store(true)

	errCh := make(chan error, 1)
	go func() {
		_, rerr := stream.Recv()
		errCh <- rerr
	}()

	select {
	case rerr := <-errCh:
		require.Error(t, rerr)
		assert.Equal(t, codes.Unavailable, status.Code(rerr), rerr)
	case <-time.After(time.Second * 20):
		require.Fail(t, "placement stream to a partitioned leader never failed")
	}
}
