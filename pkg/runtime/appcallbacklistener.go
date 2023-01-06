/*
Copyright 2021 The Dapr Authors
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
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// Timeout for accepting connections from clients before the ephemeral listener is terminated
const appCallbackConnectTimeout = 10 * time.Second

// createAppCallbackListener starts the listener for accepting app callback connections.
// It returns the port it's listening on.
func (a *DaprRuntime) createAppCallbackListener() (int, error) {
	// Create a new TCP listener that will accept connections from the client
	// It will listen on port "CallbackChannelPort" if defined
	// The port could be "0", which means that the kernel will assign a random available port
	return a.appCallbackListener.Start(a.runtimeConfig.CallbackChannelPort)
}

type appCallbackListener struct {
	OnAppCallbackConnection func(conn net.Conn)

	lock   sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc

	// Timeout for waiting apps to connect
	// The default value is 0 which will default to the "appCallbackConnectTimeout" constant
	// This option is available to override the timeout for testing
	timeout time.Duration
}

// Start the listener, returning the port it's listening on
// Parameter port can be 0 to select a rando available port
func (acl *appCallbackListener) Start(port int) (int, error) {
	// If we can't acquire a lock, it means that there's already a listener running, so we need to stop that first
	if !acl.lock.TryLock() {
		// Cancel the context, which will cause the current listener to be stopped shortly
		if acl.cancel != nil {
			acl.cancel()
		}

		// Block until we acquire a lock - i.e. until the active listener has stopped
		acl.lock.Lock()
	}

	// TODO @ItalyPaleAle: use APIListenAddresses from the config
	addr, err := net.ResolveTCPAddr("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return 0, fmt.Errorf("failed to create address: %w", err)
	}
	lis, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("failed to create listener: %w", err)
	}
	port = lis.Addr().(*net.TCPAddr).Port

	log.Debugf("Started app callback listener on port %d", port)

	// In a background goroutine, wait for the first client to establish a connection to the listener we just created
	// The first connectiopn to be established wins
	// There's also a timeout after which we will close the listener if no one connected to it
	timeout := acl.timeout
	if timeout == 0 {
		timeout = appCallbackConnectTimeout
	}
	acl.ctx, acl.cancel = context.WithTimeout(context.Background(), timeout)
	connCh := make(chan any)
	go func() {
		conn, connErr := lis.Accept()
		if connErr != nil {
			connCh <- connErr
		} else {
			connCh <- conn
		}
		close(connCh)
	}()
	go func() {
		var innerErr error
		select {
		case <-acl.ctx.Done():
			// Context was canceled, which means one of two things:
			// 1. We timed out (if we have a deadline exceeded error)
			// 2. The listener is being stopped because another caller wants to restart the process (context canceled)
			if errors.Is(acl.ctx.Err(), context.DeadlineExceeded) {
				// Timed out
				// Log, then exit the select block
				log.Warnf("Client did not connect to the app callback listener within %v", timeout)
			} else {
				// Only write a debug log
				log.Debug("App callback listener is being stopped early")
			}
		case msg := <-connCh:
			// Check if the context has been canceled in the meanwhile
			if msg == nil || acl.ctx.Err() != nil {
				// Exit the select block
				break
			}
			switch v := msg.(type) {
			case error:
				// Log, then exit the select block
				log.Errorf("Error while trying to accept connection to the app callback listener: %v", v)
			case net.Conn:
				log.Infof("Established client connection on the app callback listener from %v", v.RemoteAddr())
				acl.OnAppCallbackConnection(v)
			}
		}

		// Close the listener - whether we have a connection or not, we don't need it anymore
		innerErr = lis.Close()
		if innerErr != nil {
			log.Errorf("Failed to close app callback listener: %v", innerErr)
		}

		// Remove the lock so other callers can start a listener
		acl.lock.Unlock()
	}()

	return port, nil
}
