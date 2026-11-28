/*
Copyright 2025 The Dapr Authors
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
package static

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func TestNewStaticConnector(t *testing.T) {
	addresses := []string{"dapr1:50005", "dapr2:50005", "dapr3:50005"}
	conn, err := New(Options{Addresses: addresses})

	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr1:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr2:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr3:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr1:50005", conn.Address())
}

type blackholeConn struct {
	net.Conn
	drop *atomic.Bool
}

func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.drop.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func TestConnectDetectsPartitionedServer(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, s grpc.ServerStream) error {
		_ = s.SendHeader(nil)
		<-s.Context().Done()
		return nil
	}))
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	var drop atomic.Bool
	c, err := New(Options{
		Addresses: []string{lis.Addr().String()},
		GRPCOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
				nc, derr := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
				return &blackholeConn{Conn: nc, drop: &drop}, derr
			}),
		},
	})
	require.NoError(t, err)
	conn, err := c.Connect(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	stream, err := v1pb.NewPlacementClient(conn).ReportDaprStatus(ctx)
	require.NoError(t, err)
	_, err = stream.Header()
	require.NoError(t, err)

	drop.Store(true)
	_, err = stream.Recv()
	assert.Equal(t, codes.Unavailable, status.Code(err), err)
}
