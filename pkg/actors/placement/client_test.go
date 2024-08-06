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

package placement

import (
	"context"
	"crypto/x509"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/security"
)

func TestConnectToServer(t *testing.T) {
	t.Run("when grpc get opts return an error connectToServer should return an error", func(t *testing.T) {
		client := newPlacementClient(func() ([]grpc.DialOption, error) {
			return nil, errEstablishingTLSConn
		})
		require.Equal(t, client.connectToServer(context.Background(), ""), errEstablishingTLSConn)
	})
	t.Run("when grpc dial returns an error connectToServer should return an error", func(t *testing.T) {
		client := newPlacementClient(func() ([]grpc.DialOption, error) {
			return []grpc.DialOption{}, nil
		})

		require.Error(t, client.connectToServer(context.Background(), ""))
	})
	t.Run("when new placement stream returns an error connectToServer should return an error", func(t *testing.T) {
		client := newPlacementClient(func() ([]grpc.DialOption, error) {
			return []grpc.DialOption{}, nil
		})
		conn, cleanup := newTestServerWithOpts() // do not register the placement stream server
		defer cleanup()
		require.Error(t, client.connectToServer(context.Background(), conn))
	})
	t.Run("when connectToServer succeeds it should broadcast that a new connection is alive", func(t *testing.T) {
		conn, _, cleanup := newTestServer() // do not register the placement stream server
		defer cleanup()

		client := newPlacementClient(getGrpcOptsGetter([]string{conn}, testSecurity(t)))

		var ready sync.WaitGroup
		ready.Add(1)
		go func() {
			client.waitUntil(func(streamConnAlive bool) bool {
				return streamConnAlive
			})
			ready.Done()
		}()

		require.NoError(t, client.connectToServer(context.Background(), conn))
		ready.Wait() // should not timeout
		assert.True(t, client.streamConnAlive)
	})

	t.Run("when connectToServer succeeds it should correctly set the stream metadata", func(t *testing.T) {
		conn, _, cleanup := newTestServer() // do not register the placement stream server
		defer cleanup()

		client := newPlacementClient(getGrpcOptsGetter([]string{conn}, testSecurity(t)))

		var ready sync.WaitGroup
		ready.Add(1)
		go func() {
			client.waitUntil(func(streamConnAlive bool) bool {
				return streamConnAlive
			})
			ready.Done()
		}()

		err := client.connectToServer(context.Background(), conn)
		require.NoError(t, err)

		// Extract the "dapr-accept-vnodes" value from the context's metadata
		md, ok := metadata.FromOutgoingContext(client.clientStream.Context())
		require.True(t, ok)

		requiresVnodes, ok := md[placement.GRPCContextKeyAcceptVNodes]
		require.True(t, ok)
		require.Len(t, requiresVnodes, 1)

		assert.Equal(t, "false", requiresVnodes[0])
	})

	t.Run("when connectToServer tries to connect to a different address the client conn should not be reused", func(t *testing.T) {
		addr1, _, cleanup1 := newTestServer() // do not register the placement stream server
		defer cleanup1()
		addr2, _, cleanup2 := newTestServer() // do not register the placement stream server
		defer cleanup2()

		client := newPlacementClient(getGrpcOptsGetter([]string{addr1}, testSecurity(t)))
		require.NoError(t, client.connectToServer(context.Background(), addr1))
		require.Equal(t, client.clientConn.Target(), addr1)

		conn1 := client.clientConn

		require.NoError(t, client.connectToServer(context.Background(), addr2))
		require.NotEqualf(t, conn1, client.clientConn, "client conn should not be reused")
	})

	t.Run("when connectToServer tries to connect to the same address the client conn should be reused", func(t *testing.T) {
		addr, _, cleanup := newTestServer() // do not register the placement stream server
		defer cleanup()

		client := newPlacementClient(getGrpcOptsGetter([]string{addr}, testSecurity(t)))
		require.NoError(t, client.connectToServer(context.Background(), addr))
		require.Equal(t, client.clientConn.Target(), addr)

		conn := client.clientConn

		require.NoError(t, client.connectToServer(context.Background(), addr))
		require.Equal(t, conn, client.clientConn, "client should be reused")
	})
}

func TestDisconnect(t *testing.T) {
	t.Run("disconnectFn should return and broadcast when connection is not alive", func(t *testing.T) {
		client := newPlacementClient(func() ([]grpc.DialOption, error) {
			return nil, nil
		})
		client.streamConnAlive = true

		called := false
		shouldNotBeCalled := func() {
			called = true
		}
		var ready sync.WaitGroup
		ready.Add(1)

		go func() {
			client.waitUntil(func(streamConnAlive bool) bool {
				return !streamConnAlive
			})
			ready.Done()
		}()
		client.streamConnAlive = false
		client.disconnectFn(shouldNotBeCalled, true)
		ready.Wait()
		assert.False(t, called)
	})
	t.Run("disconnectFn should broadcast not connected when disconnected and should drain and execute func inside lock", func(t *testing.T) {
		conn, _, cleanup := newTestServer() // do not register the placement stream server
		defer cleanup()

		client := newPlacementClient(getGrpcOptsGetter([]string{conn}, testSecurity(t)))
		require.NoError(t, client.connectToServer(context.Background(), conn))

		called := false
		shouldBeCalled := func() {
			called = true
		}

		var ready sync.WaitGroup
		ready.Add(1)

		go func() {
			client.waitUntil(func(streamConnAlive bool) bool {
				return !streamConnAlive
			})
			ready.Done()
		}()
		client.disconnectFn(shouldBeCalled, true)
		ready.Wait()
		assert.Equal(t, connectivity.Shutdown, client.clientConn.GetState())
		assert.True(t, called)
	})
	t.Run("disconnectFn should broadcast not connected when disconnected and should drain and execute func inside lock, reusing connection", func(t *testing.T) {
		conn, _, cleanup := newTestServer() // do not register the placement stream server
		defer cleanup()

		client := newPlacementClient(getGrpcOptsGetter([]string{conn}, testSecurity(t)))
		require.NoError(t, client.connectToServer(context.Background(), conn))

		called := false
		shouldBeCalled := func() {
			called = true
		}

		var ready sync.WaitGroup
		ready.Add(1)

		go func() {
			client.waitUntil(func(streamConnAlive bool) bool {
				return !streamConnAlive
			})
			ready.Done()
		}()
		client.disconnectFn(shouldBeCalled, false)
		ready.Wait()
		assert.Equal(t, connectivity.Ready, client.clientConn.GetState())
		assert.True(t, called)
	})
}

func testSecurity(t *testing.T) security.Handler {
	secP, err := security.New(context.Background(), security.Options{
		TrustAnchors:            []byte("test"),
		AppID:                   "test",
		ControlPlaneTrustDomain: "test.example.com",
		ControlPlaneNamespace:   "default",
		MTLSEnabled:             false,
		OverrideCertRequestFn: func(context.Context, []byte) ([]*x509.Certificate, error) {
			return []*x509.Certificate{nil}, nil
		},
		Healthz: healthz.New(),
	})
	require.NoError(t, err)
	go secP.Run(context.Background())
	sec, err := secP.Handler(context.Background())
	require.NoError(t, err)

	return sec
}
