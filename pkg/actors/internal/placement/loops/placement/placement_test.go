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

package placement

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

func TestHandleCloseStream_NotReady(t *testing.T) {
	t.Run("handleCloseStream sets ready to false and closes diss loop", func(t *testing.T) {
		ht := healthzfake.New()

		ready := &atomic.Bool{}
		ready.Store(true)

		ctx, cancel := context.WithCancel(t.Context())
		// Cancel context immediately so handleReconnect exits quickly.
		cancel()

		var dissLoopClosed atomic.Bool
		dissLoop := loopfake.New[loops.EventDiss]().
			WithClose(func(loops.EventDiss) {
				dissLoopClosed.Store(true)
			})

		p := &placement{
			id:         "test-id",
			namespace:  "default",
			ready:      ready,
			htarget:    ht,
			dissLoop:   dissLoop,
			actorTable: tablefake.New(),
			inflight:   inflight.New(inflight.Options{Hostname: "localhost", Port: "3500"}),
			idx:        1,
		}

		err := p.handleCloseStream(ctx, &loops.ConnCloseStream{
			Error: errors.New("connection lost"),
			IDx:   1,
		})

		// Should return context.Canceled since we cancelled the context.
		require.Error(t, err)
		assert.Equal(t, context.Canceled, err)

		assert.False(t, ready.Load(),
			"ready flag should be false when stream closes")
		assert.True(t, dissLoopClosed.Load(),
			"dissemination loop should be closed")
	})

	t.Run("handleCloseStream with mismatched idx is ignored", func(t *testing.T) {
		ht := healthzfake.New()

		ready := &atomic.Bool{}
		ready.Store(true)

		p := &placement{
			id:        "test-id",
			namespace: "default",
			ready:     ready,
			htarget:   ht,
			idx:       2,
		}

		err := p.handleCloseStream(t.Context(), &loops.ConnCloseStream{
			Error: errors.New("connection lost"),
			IDx:   1, // Mismatched idx.
		})
		require.NoError(t, err)

		assert.True(t, ready.Load(),
			"ready flag should remain true when close stream idx doesn't match")
	})
}

// dialConnector dials a new connection to addr on every Connect, as the
// static and DNS connectors do, and records each one.
type dialConnector struct {
	t     *testing.T
	addr  string
	mu    sync.Mutex
	conns []*grpc.ClientConn
}

func (d *dialConnector) Connect(context.Context) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient("passthrough:///"+d.addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}
	d.t.Cleanup(func() { conn.Close() })

	d.mu.Lock()
	defer d.mu.Unlock()
	d.conns = append(d.conns, conn)
	return conn, nil
}

func (d *dialConnector) Address() string { return d.addr }

// open returns the number of recorded connections which are not closed.
func (d *dialConnector) open() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	var n int
	for _, conn := range d.conns {
		if conn.GetState() != connectivity.Shutdown {
			n++
		}
	}
	return n
}

// holdServer keeps each ReportDaprStatus stream open until the client ends it.
type holdServer struct {
	v1pb.UnimplementedPlacementServer
}

func (holdServer) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	<-stream.Context().Done()
	return nil
}

// TestTryConnectClosesConn asserts tryConnect closes the connection when the
// stream fails to open and when the stream ends. Every attempt dials a new
// connection, so a connection left open keeps redialing placement.
func TestTryConnectClosesConn(t *testing.T) {
	t.Parallel()

	t.Run("streams which fail to open close their connections", func(t *testing.T) {
		t.Parallel()
		// Nothing listens on port 1: the dial succeeds and opening the
		// stream fails, as when placement is down.
		d := &dialConnector{t: t, addr: "127.0.0.1:1"}
		p := &placement{connector: d}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		for range 5 {
			_, _, err := p.tryConnect(ctx)
			require.Error(t, err)
		}
		assert.Len(t, d.conns, 5)
		assert.Zero(t, d.open())
	})

	t.Run("ending the stream closes the connection", func(t *testing.T) {
		t.Parallel()
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		srv := grpc.NewServer()
		v1pb.RegisterPlacementServer(srv, holdServer{})
		go srv.Serve(lis)
		t.Cleanup(srv.Stop)

		d := &dialConnector{t: t, addr: lis.Addr().String()}
		p := &placement{connector: d}
		client, streamCancel, err := p.tryConnect(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, d.open())

		streamCancel()
		require.ErrorIs(t, client.Context().Err(), context.Canceled)
		assert.Zero(t, d.open())
	})
}
