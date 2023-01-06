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
	"fmt"
	"net"
	"time"
)

// Timeout for accepting connections from clients before the ephemeral listener is terminated
const appCallbackConnectTimeout = 10 * time.Second

// createAppCallbackListener starts the listener for accepting app callback connections.
// It returns the port it's listening on.
func (a *DaprRuntime) createAppCallbackListener() (int, error) {
	// Create a new TCP listener that will accept connections from the client
	// This is listening on port "0" which means that the kernel will assign a random available port
	// TODO @ItalyPaleAle: use APIListenAddresses from the config
	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to create address: %w", err)
	}
	lis, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("failed to create listener: %w", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port

	log.Debugf("Created ephemeral listener on port %d", port)

	// In a background goroutine, wait for the first client to establish a connection to the listener we just created
	// The first connectiopn to be established wins
	// There's also a timeout after which we will close the listener if no one connected to it
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
		select {
		case <-time.After(appCallbackConnectTimeout):
			// Timed out
			// Log, then exit the select block
			log.Warnf("Client did not connect to the ephemeral listener within %v", appCallbackConnectTimeout)
		case msg := <-connCh:
			if msg == nil {
				// Exit the select block
				break
			}
			switch v := msg.(type) {
			case error:
				// Log, then exit the select block
				log.Errorf("Error while trying to accept connection to the ephemeral listener: %v", v)
			case net.Conn:
				log.Infof("Established client connection on the ephemeral listener from %v", v.RemoteAddr())
				a.onAppCallbackConnection(v)
			}
		}

		// Close the listener - whether we have a connection or not, we don't need it anymore
		innerErr := lis.Close()
		if innerErr != nil {
			log.Errorf("Failed to close epehemeral listener: %v", innerErr)
		}
	}()

	return port, nil
}
