/*
Copyright 2023 The Dapr Authors
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

package runtime

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/logger"
)

func TestAppCallbackListener(t *testing.T) {
	t.Run("create listener on specific port", func(t *testing.T) {
		connected := make(chan struct{}, 1)
		acl := &appCallbackListener{
			OnAppCallbackConnection: func(conn net.Conn) {
				close(connected)
			},
			timeout: 500 * time.Millisecond,
		}

		// Get an available port
		reqPort := freeport.GetPort()

		// Start the listener
		gotPort, err := acl.Start(reqPort)
		require.NoError(t, err, "listener did not start correctly")
		assert.Equal(t, reqPort, gotPort)

		// Connect - should trigger OnAppCallbackConnection
		addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("localhost:%d", gotPort))
		require.NoErrorf(t, err, "could not resolve TCP address localhost:%d", gotPort)
		conn, err := net.DialTCP("tcp", nil, addr)
		require.NoError(t, err, "cannot establish connection")
		defer conn.Close()

		select {
		case <-connected:
			// All good
		case <-time.After(500 * time.Millisecond):
			t.Fatal("OnAppCallbackConnection was not invoked within 500ms")
		}
	})

	t.Run("create listener on random port", func(t *testing.T) {
		connected := make(chan struct{}, 1)
		acl := &appCallbackListener{
			OnAppCallbackConnection: func(conn net.Conn) {
				close(connected)
			},
			timeout: 500 * time.Millisecond,
		}

		// Start the listener with a random port
		gotPort, err := acl.Start(0)
		require.NoError(t, err, "listener did not start correctly")
		assert.Greater(t, gotPort, 0)

		// Connect - should trigger OnAppCallbackConnection
		addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("localhost:%d", gotPort))
		require.NoErrorf(t, err, "could not resolve TCP address localhost:%d", gotPort)
		conn, err := net.DialTCP("tcp", nil, addr)
		require.NoError(t, err, "cannot establish connection")
		defer conn.Close()

		select {
		case <-connected:
			// All good
		case <-time.After(500 * time.Millisecond):
			t.Fatal("OnAppCallbackConnection was not invoked within 500ms")
		}
	})

	t.Run("timed out waiting for connections", func(t *testing.T) {
		const timeout = 500 * time.Millisecond
		acl := &appCallbackListener{
			timeout: timeout,
		}

		// Replace the logger to capture logs
		prev := log
		logDest := &bytes.Buffer{}
		log = newTestLogger(t, logDest)
		defer func() {
			log = prev
		}()

		// Start the listener with a random port
		gotPort, err := acl.Start(0)
		require.NoError(t, err, "listener did not start correctly")
		assert.Greater(t, gotPort, 0)

		// Wait 1s (2x the timeout)
		time.Sleep(2 * timeout)

		// Connections should fail
		addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("localhost:%d", gotPort))
		require.NoErrorf(t, err, "could not resolve TCP address localhost:%d", gotPort)
		_, err = net.DialTCP("tcp", nil, addr)
		require.Error(t, err, "should not have been able to connect")
		assert.ErrorContains(t, err, "connection refused")

		// Log should contain a mention of the timeout
		logs := logDest.String()
		idx1 := strings.Index(logs, "Client did not connect to the app callback listener within")
		assert.Greater(t, idx1, -1)
		idx2 := strings.Index(logs, "Closing app callback listener")
		assert.Greater(t, idx2, idx1)
	})

	t.Run("second request should stop running listener", func(t *testing.T) {
		connected := make(chan struct{}, 1)
		acl := &appCallbackListener{
			// Long timeout here
			timeout: 30 * time.Second,
			OnAppCallbackConnection: func(conn net.Conn) {
				close(connected)
			},
		}

		// Replace the logger to capture logs
		prev := log
		logDest := &bytes.Buffer{}
		log = newTestLogger(t, logDest)
		defer func() {
			log = prev
		}()

		// Start the listener with a random port
		gotPort1, err := acl.Start(0)
		require.NoError(t, err, "listener did not start correctly")
		assert.Greater(t, gotPort1, 0)

		// Start the listener again
		// This time we should get a different port
		// (There's a chance that the random port could be the same as the last one, but it's a very slim one)
		start := time.Now()
		gotPort2, err := acl.Start(0)
		require.NoError(t, err, "listener did not start correctly")
		// Should take less than 15s, i.e. less than the timeout (I'm being very generous here with timeouts)
		assert.Less(t, time.Since(start), 15*time.Second)
		assert.Greater(t, gotPort2, 0)
		assert.NotEqual(t, gotPort2, gotPort1)

		// Connections to gotPort1 should fail
		addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("localhost:%d", gotPort1))
		require.NoErrorf(t, err, "could not resolve TCP address localhost:%d", gotPort1)
		_, err = net.DialTCP("tcp", nil, addr)
		require.Error(t, err, "should not have been able to connect")
		assert.ErrorContains(t, err, "connection refused")

		// Connections to gotPort2 should succeed
		addr, err = net.ResolveTCPAddr("tcp", fmt.Sprintf("localhost:%d", gotPort2))
		require.NoErrorf(t, err, "could not resolve TCP address localhost:%d", gotPort2)
		conn, err := net.DialTCP("tcp", nil, addr)
		require.NoError(t, err, "cannot establish connection")
		defer conn.Close()

		select {
		case <-connected:
			// All good
		case <-time.After(500 * time.Millisecond):
			t.Fatal("OnAppCallbackConnection was not invoked within 500ms")
		}

		// Check logs
		logs := logDest.String()
		idxs := []int{
			strings.Index(logs, fmt.Sprintf("Started app callback listener on port %d", gotPort1)),
			strings.Index(logs, "App callback listener is being stopped early"),
			strings.Index(logs, fmt.Sprintf("Closing app callback listener on port %d", gotPort1)),
			strings.Index(logs, fmt.Sprintf("Started app callback listener on port %d", gotPort2)),
			strings.Index(logs, "Established client connection on the app callback listener"),
			strings.Index(logs, fmt.Sprintf("Closing app callback listener on port %d", gotPort2)),
		}
		for i := 0; i < len(idxs); i++ {
			assert.Greaterf(t, idxs[i], -1, "message %d was not found in the logs", i)
			if i > 0 {
				assert.Greaterf(t, idxs[i], idxs[i-1], "message %d came before %d", i, i-1)
			}
		}
	})
}

func newTestLogger(t *testing.T, dest *bytes.Buffer) logger.Logger {
	t.Helper()

	testLogger := logger.NewLogger("testlog")
	testLogger.EnableJSONOutput(true)
	testLogger.SetOutputLevel(logger.DebugLevel)
	testLogger.SetOutput(dest)
	return testLogger
}
