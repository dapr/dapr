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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

func TestConnectToServer(t *testing.T) {
	t.Run("when grpc get opts return an error connectToServer should return an error", func(t *testing.T) {
		client := newPlacementClient(func() ([]grpc.DialOption, error) {
			return nil, errEstablishingTLSConn
		})
		assert.Equal(t, client.connectToServer(""), errEstablishingTLSConn)
	})
	t.Run("when grpc dial returns an error connectToServer should return an error", func(t *testing.T) {
		client := newPlacementClient(func() ([]grpc.DialOption, error) {
			return []grpc.DialOption{}, nil
		})

		assert.NotNil(t, client.connectToServer(""))
	})
	t.Run("when new placement stream returns an error connectToServer should return an error", func(t *testing.T) {
		client := newPlacementClient(func() ([]grpc.DialOption, error) {
			return []grpc.DialOption{}, nil
		})
		conn, cleanup := newTestServerWithOpts() // do not register the placement stream server
		defer cleanup()
		assert.NotNil(t, client.connectToServer(conn))
	})
	t.Run("when connectToServer succeeds it should broadcast that a new connection is alive", func(t *testing.T) {
		conn, _, cleanup := newTestServer() // do not register the placement stream server
		defer cleanup()

		client := newPlacementClient(getGrpcOptsGetter([]string{conn}, nil))

		var ready sync.WaitGroup
		ready.Add(1)
		go func() {
			client.waitUntil(func(streamConnAlive bool) bool {
				return streamConnAlive
			})
			ready.Done()
		}()

		assert.Nil(t, client.connectToServer(conn))
		ready.Wait() // should not timeout
		assert.True(t, client.streamConnAlive)
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
		client.disconnectFn(shouldNotBeCalled)
		ready.Wait()
		assert.False(t, called)
	})
	t.Run("disconnectFn should broadcast not connected when disconnected and should drain and execute func inside lock", func(t *testing.T) {
		conn, _, cleanup := newTestServer() // do not register the placement stream server
		defer cleanup()

		client := newPlacementClient(getGrpcOptsGetter([]string{conn}, nil))
		assert.Nil(t, client.connectToServer(conn))

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
		client.disconnectFn(shouldBeCalled)
		ready.Wait()
		assert.Equal(t, client.clientConn.GetState(), connectivity.Shutdown)
		assert.True(t, called)
	})
}
